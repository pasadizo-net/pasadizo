#!/usr/bin/env python3
"""Wire compatibility of Pasadizo with upstream sing-box, on 127.0.0.1 only.

Usage:
  wire-compat.py --pasadizo PATH --upstream LABEL=PATH [--upstream ...]
                 [--only REGEX] [--naive]

For each protocol, a Pasadizo server talks to each upstream client, and each
upstream server to a Pasadizo client, with the options the house's servers and
clients use. Each pair must carry:
  tcp: a 1 MiB HTTP GET through the client's SOCKS5 port, checked by sha256;
  udp: a DNS query through SOCKS5 UDP ASSOCIATE to a local DNS responder.

Certificates, ECH keys (sing-box's PEM labels and RFC 9934) and the REALITY key
pair are made in a temporary directory with openssl and the binaries' own
`generate` commands. TCP Brutal is not tested: it needs the tcp-brutal kernel
module on the server. NaiveProxy (--naive) needs libcronet.so beside every
binary. The exit status is 1 when a pair fails.
"""

import argparse
import hashlib
import http.server
import json
import os
import re
import socket
import socketserver
import struct
import subprocess
import sys
import tempfile
import threading
import time
from collections.abc import Callable
from pathlib import Path

BLOB = os.urandom(1 << 20)
BLOB_SHA = hashlib.sha256(BLOB).hexdigest()
DNS_ANSWER = bytes([10, 11, 12, 13])


def free_port(kind: int) -> int:
    with socket.socket(socket.AF_INET, kind) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def pem(label: str, body: str) -> list[str]:
    return f"-----BEGIN {label}-----\n{body}-----END {label}-----".splitlines()


def pem_blocks(text: str) -> list[tuple[str, str]]:
    return re.findall(r"-----BEGIN ([A-Z ]+)-----\n(.*?)-----END \1-----", text, re.S)


class Material:
    """Keys and certificates for one run."""

    def __init__(self, work: Path, pasadizo: str, upstream: str) -> None:
        self.work = work

        def sh(*args: str) -> str:
            return subprocess.run(args, check=True, capture_output=True, text=True, cwd=work).stdout

        sh("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes",
           "-keyout", "ca.key", "-out", "ca.pem", "-days", "2", "-subj", "/CN=Compat CA",
           "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign")
        sh("openssl", "req", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes",
           "-keyout", "leaf.key", "-out", "leaf.csr", "-subj", "/CN=test.local")
        (work / "ext.cnf").write_text("subjectAltName=DNS:test.local,DNS:localhost,IP:127.0.0.1\n"
                                      "basicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth\n")
        sh("openssl", "x509", "-req", "-in", "leaf.csr", "-CA", "ca.pem", "-CAkey", "ca.key", "-CAcreateserial",
           "-out", "leaf.pem", "-days", "2", "-extfile", "ext.cnf")
        (work / "chain.pem").write_text((work / "leaf.pem").read_text() + (work / "ca.pem").read_text())
        self.ca = str(work / "ca.pem")
        self.chain = str(work / "chain.pem")
        self.key = str(work / "leaf.key")

        sb = dict(pem_blocks(sh(upstream, "generate", "ech-keypair", "outer.test")))
        self.ech_sb_config = pem("ECH CONFIGS", sb["ECH CONFIGS"])
        self.ech_sb_key = pem("ECH KEYS", sb["ECH KEYS"])
        rfc = pem_blocks(sh(pasadizo, "generate", "ech-keypair", "--format", "rfc9934", "outer.test"))
        self.ech_rfc_config = pem("ECHCONFIG", rfc[0][1])
        self.ech_rfc_key = pem("PRIVATE KEY", rfc[1][1]) + pem("ECHCONFIG", rfc[2][1])
        # The same ECHConfigList under the other label.
        self.ech_rfc_as_sb = pem("ECH CONFIGS", rfc[0][1])
        self.ech_sb_as_rfc = pem("ECHCONFIG", sb["ECH CONFIGS"])

        reality = sh(upstream, "generate", "reality-keypair")
        self.reality_private = re.search(r"PrivateKey: (\S+)", reality).group(1)
        self.reality_public = re.search(r"PublicKey: (\S+)", reality).group(1)
        self.uuid = sh(upstream, "generate", "uuid").strip()
        self.ss_key = sh(upstream, "generate", "rand", "--base64", "16").strip()
        self.reality_target_port = free_port(socket.SOCK_STREAM)

    def server_tls(self, **extra: object) -> dict:
        return {"enabled": True, "server_name": "test.local", "certificate_path": self.chain,
                "key_path": self.key, **extra}

    def client_tls(self, **extra: object) -> dict:
        return {"enabled": True, "server_name": "test.local", "certificate_path": self.ca, **extra}


UTLS = {"enabled": True, "fingerprint": "chrome"}

# build(material, port, server_is_pasadizo, client_is_pasadizo)
#   -> (inbound, outbound, "tcp" or "udp" for the server's listener)
Build = Callable[[Material, int, bool, bool], tuple[dict, dict, str]]


def hysteria2(obfs: str, careful_resume: bool = False) -> Build:
    def build(m: Material, port: int, p_srv: bool, p_cli: bool) -> tuple[dict, dict, str]:
        s = {"type": "hysteria2", "listen": "127.0.0.1", "listen_port": port,
             "users": [{"name": "u", "password": "hy2-password"}], "tls": m.server_tls(alpn=["h3"])}
        c = {"type": "hysteria2", "server": "127.0.0.1", "server_port": port, "password": "hy2-password",
             "tls": m.client_tls(alpn=["h3"])}
        if obfs == "salamander":
            s["obfs"] = c["obfs"] = {"type": "salamander", "password": "obfs-password"}
            c["up_mbps"], c["down_mbps"] = 50, 100
        else:
            s["obfs"] = c["obfs"] = {"type": "gecko", "password": "obfs-password",
                                     "min_packet_size": 512, "max_packet_size": 1200}
        # Careful Resume is Pasadizo's: only a Pasadizo peer gets it, and the
        # upstream peer must not notice.
        if careful_resume and p_srv:
            s["careful_resume"] = {"enabled": True, "lifetime": "10m",
                                   "state_file": str(m.work / f"careful-resume-{port}.json")}
        if careful_resume and p_cli:
            c["careful_resume"] = {"enabled": True}
        return s, c, "udp"
    return build


def anytls(ech: str) -> Build:
    def build(m: Material, port: int, p_srv: bool, p_cli: bool) -> tuple[dict, dict, str]:
        s = {"type": "anytls", "listen": "127.0.0.1", "listen_port": port,
             "users": [{"name": "u", "password": "anytls-password"}],
             "tls": m.server_tls(alpn=["h2", "http/1.1"])}
        c = {"type": "anytls", "server": "127.0.0.1", "server_port": port, "password": "anytls-password",
             "tls": m.client_tls(utls=UTLS)}
        if ech == "sing-box":
            s["tls"]["ech"] = {"enabled": True, "key": m.ech_sb_key}
            c["tls"]["ech"] = {"enabled": True, "config": m.ech_sb_config}
        elif ech == "rfc9934":
            # Pasadizo reads RFC 9934 files; upstream only its own labels, so
            # an upstream peer gets the same key material under those.
            if p_srv:
                s["tls"]["ech"] = {"enabled": True, "key": m.ech_rfc_key}
                c["tls"]["ech"] = {"enabled": True, "config": m.ech_rfc_config if p_cli else m.ech_rfc_as_sb}
            else:
                s["tls"]["ech"] = {"enabled": True, "key": m.ech_sb_key}
                c["tls"]["ech"] = {"enabled": True, "config": m.ech_sb_as_rfc if p_cli else m.ech_sb_config}
        return s, c, "tcp"
    return build


def websocket(protocol: str) -> Build:
    def build(m: Material, port: int, p_srv: bool, p_cli: bool) -> tuple[dict, dict, str]:
        transport = {"type": "ws", "path": "/assets/stream"}
        if protocol == "vless":
            transport |= {"max_early_data": 2048, "early_data_header_name": "Sec-WebSocket-Protocol"}
            s = {"type": "vless", "users": [{"name": "u", "uuid": m.uuid}]}
            c = {"type": "vless", "uuid": m.uuid}
        else:
            s = {"type": "trojan", "users": [{"name": "u", "password": "trojan-password"}]}
            c = {"type": "trojan", "password": "trojan-password"}
        s |= {"listen": "127.0.0.1", "listen_port": port, "tls": m.server_tls(), "transport": transport,
              "multiplex": {"enabled": True, "padding": False}}
        c |= {"server": "127.0.0.1", "server_port": port, "tls": m.client_tls(utls=UTLS), "transport": transport,
              "multiplex": {"enabled": True, "protocol": "h2mux", "padding": False}}
        return s, c, "tcp"
    return build


def shadowsocks(m: Material, port: int, p_srv: bool, p_cli: bool) -> tuple[dict, dict, str]:
    method = "2022-blake3-aes-128-gcm"
    s = {"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": port, "method": method, "password": m.ss_key}
    c = {"type": "shadowsocks", "server": "127.0.0.1", "server_port": port, "method": method, "password": m.ss_key}
    return s, c, "tcp"


def tuic(m: Material, port: int, p_srv: bool, p_cli: bool) -> tuple[dict, dict, str]:
    s = {"type": "tuic", "listen": "127.0.0.1", "listen_port": port,
         "users": [{"name": "u", "uuid": m.uuid, "password": "tuic-password"}], "congestion_control": "bbr",
         "auth_timeout": "3s", "heartbeat": "10s", "tls": m.server_tls(alpn=["h3"])}
    c = {"type": "tuic", "server": "127.0.0.1", "server_port": port, "uuid": m.uuid, "password": "tuic-password",
         "congestion_control": "bbr", "tls": m.client_tls(alpn=["h3"])}
    return s, c, "udp"


def reality(m: Material, port: int, p_srv: bool, p_cli: bool) -> tuple[dict, dict, str]:
    s = {"type": "vless", "listen": "127.0.0.1", "listen_port": port,
         "users": [{"name": "u", "uuid": m.uuid, "flow": "xtls-rprx-vision"}],
         "tls": {"enabled": True, "server_name": "test.local",
                 "reality": {"enabled": True,
                             "handshake": {"server": "127.0.0.1", "server_port": m.reality_target_port},
                             "private_key": m.reality_private, "short_id": ["0123abcd"]}}}
    c = {"type": "vless", "server": "127.0.0.1", "server_port": port, "uuid": m.uuid, "flow": "xtls-rprx-vision",
         "tls": {"enabled": True, "server_name": "test.local", "utls": UTLS,
                 "reality": {"enabled": True, "public_key": m.reality_public, "short_id": "0123abcd"}}}
    return s, c, "tcp"


def naive(m: Material, port: int, p_srv: bool, p_cli: bool) -> tuple[dict, dict, str]:
    s = {"type": "naive", "listen": "127.0.0.1", "listen_port": port,
         "users": [{"username": "u", "password": "naive-password"}], "tls": m.server_tls()}
    # Cronet resolves server_name, so the client dials 127.0.0.1 without one.
    c = {"type": "naive", "server": "127.0.0.1", "server_port": port, "username": "u", "password": "naive-password",
         "udp_over_tcp": True, "tls": {"enabled": True, "certificate_path": m.ca}}
    return s, c, "tcp"


CASES: dict[str, Build] = {
    "hysteria2-salamander": hysteria2("salamander"),
    "hysteria2-gecko": hysteria2("gecko"),
    "hysteria2-gecko-careful-resume": hysteria2("gecko", careful_resume=True),
    "anytls": anytls(""),
    "anytls-ech-sing-box-pem": anytls("sing-box"),
    "anytls-ech-rfc9934-pem": anytls("rfc9934"),
    "trojan-ws-h2mux": websocket("trojan"),
    "vless-ws-h2mux": websocket("vless"),
    "shadowsocks-2022": shadowsocks,
    "tuic": tuic,
    "vless-reality-vision": reality,
}


class Blob(http.server.BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        self.send_response(200)
        self.send_header("Content-Length", str(len(BLOB)))
        self.end_headers()
        self.wfile.write(BLOB)

    def log_message(self, *args: object) -> None:
        pass


class HTTPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True


def serve_dns(sock: socket.socket) -> None:
    while True:
        query, addr = sock.recvfrom(2048)
        if len(query) < 17:
            continue
        end = 12
        while query[end] != 0:
            end += query[end] + 1
        header = query[:2] + b"\x81\x80\x00\x01\x00\x01\x00\x00\x00\x00"
        answer = b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c\x00\x04" + DNS_ANSWER
        sock.sendto(header + query[12:end + 5] + answer, addr)


def check_tcp(mixed: int, http_port: int) -> str:
    env = {k: v for k, v in os.environ.items() if "proxy" not in k.lower()}
    p = subprocess.run(["curl", "-s", "--max-time", "15", "--socks5-hostname", f"127.0.0.1:{mixed}",
                        f"http://127.0.0.1:{http_port}/blob"], capture_output=True, env=env)
    if p.returncode != 0:
        return f"curl exit {p.returncode}"
    return "ok" if hashlib.sha256(p.stdout).hexdigest() == BLOB_SHA else f"wrong body ({len(p.stdout)} bytes)"


def check_udp(mixed: int, dns_port: int) -> str:
    name = b"".join(bytes([len(part)]) + part for part in b"compat.test".split(b".")) + b"\x00"
    try:
        with socket.create_connection(("127.0.0.1", mixed), timeout=5) as ctl, \
                socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as udp:
            ctl.sendall(b"\x05\x01\x00")
            if ctl.recv(2) != b"\x05\x00":
                return "SOCKS greeting refused"
            ctl.sendall(b"\x05\x03\x00\x01\x00\x00\x00\x00\x00\x00")
            reply = ctl.recv(10)
            if len(reply) < 10 or reply[1] != 0:
                return "UDP ASSOCIATE refused"
            relay_host = socket.inet_ntoa(reply[4:8])
            relay = ("127.0.0.1" if relay_host == "0.0.0.0" else relay_host, struct.unpack(">H", reply[8:10])[0])
            udp.settimeout(3)
            for attempt in range(4):
                txid = 0x4000 + attempt
                query = struct.pack(">HHHHHH", txid, 0x0100, 1, 0, 0, 0) + name + b"\x00\x01\x00\x01"
                udp.sendto(b"\x00\x00\x00\x01" + socket.inet_aton("127.0.0.1") + struct.pack(">H", dns_port) + query,
                           relay)
                try:
                    data = udp.recv(2048)[10:]
                except TimeoutError:
                    continue
                ok = struct.unpack(">H", data[:2])[0] == txid and data.endswith(DNS_ANSWER)
                return "ok" if ok else "wrong DNS answer"
            return "no DNS answer"
    except OSError as e:
        return f"error: {e}"


def listening(port: int, proto: str, proc: subprocess.Popen, seconds: float = 10) -> bool:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            return False
        flag = "-Hlun" if proto == "udp" else "-Hltn"
        if subprocess.run(["ss", flag, f"sport = :{port}"], capture_output=True, text=True).stdout.strip():
            return True
        time.sleep(0.1)
    return False


def log_tail(path: Path) -> str:
    lines = [re.sub(r"\x1b\[[0-9;]*m", "", line) for line in path.read_text().splitlines()]
    errors = [line for line in lines if re.search(r"ERROR|FATAL", line)] or lines
    return " | ".join(line[-200:] for line in errors[-2:])


def run_pair(m: Material, build: Build, server: tuple[str, str], client: tuple[str, str], pasadizo: str,
             http_port: int, dns_port: int) -> tuple[str, str]:
    p_srv, p_cli = server[1] == pasadizo, client[1] == pasadizo
    proto_port = free_port(socket.SOCK_STREAM)
    mixed = free_port(socket.SOCK_STREAM)
    inbound, outbound, proto = build(m, proto_port, p_srv, p_cli)
    name = f"{server[0]}-{client[0]}-{proto_port}"
    server_config = {"log": {"level": "warn"}, "inbounds": [{"tag": "in", **inbound}],
                     "outbounds": [{"type": "direct", "tag": "direct"}]}
    client_config = {"log": {"level": "warn"},
                     "inbounds": [{"type": "mixed", "tag": "mixed", "listen": "127.0.0.1", "listen_port": mixed}],
                     "outbounds": [{"tag": "proxy", **outbound}], "route": {"final": "proxy"}}
    paths = {}
    for role, config in (("server", server_config), ("client", client_config)):
        paths[role] = m.work / f"{name}.{role}.json"
        paths[role].write_text(json.dumps(config))
    procs: list[subprocess.Popen] = []
    try:
        with open(m.work / f"{name}.server.log", "w") as server_log, \
                open(m.work / f"{name}.client.log", "w") as client_log:
            procs.append(subprocess.Popen([server[1], "run", "-c", str(paths["server"])],
                                          stdout=server_log, stderr=subprocess.STDOUT))
            if not listening(proto_port, proto, procs[-1]):
                return "server did not start: " + log_tail(m.work / f"{name}.server.log"), "-"
            procs.append(subprocess.Popen([client[1], "run", "-c", str(paths["client"])],
                                          stdout=client_log, stderr=subprocess.STDOUT))
            if not listening(mixed, "tcp", procs[-1]):
                return "client did not start: " + log_tail(m.work / f"{name}.client.log"), "-"
            tcp, udp = check_tcp(mixed, http_port), check_udp(mixed, dns_port)
            if tcp != "ok" or udp != "ok":
                tcp += (f" [client: {log_tail(m.work / f'{name}.client.log')}]"
                        f" [server: {log_tail(m.work / f'{name}.server.log')}]")
            return tcp, udp
    finally:
        for proc in reversed(procs):
            proc.terminate()
            try:
                proc.wait(5)
            except subprocess.TimeoutExpired:
                proc.kill()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--pasadizo", required=True)
    parser.add_argument("--upstream", action="append", required=True, metavar="LABEL=PATH")
    parser.add_argument("--only", default="", metavar="REGEX")
    parser.add_argument("--naive", action="store_true")
    args = parser.parse_args()
    pasadizo = str(Path(args.pasadizo).resolve())
    upstreams = [(label, str(Path(path).resolve())) for label, path in (u.split("=", 1) for u in args.upstream)]
    cases = dict(CASES, naive=naive) if args.naive else CASES

    http_server = HTTPServer(("127.0.0.1", 0), Blob)
    threading.Thread(target=http_server.serve_forever, daemon=True).start()
    dns = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    dns.bind(("127.0.0.1", 0))
    threading.Thread(target=serve_dns, args=(dns,), daemon=True).start()

    failed = 0
    with tempfile.TemporaryDirectory(prefix="wire-compat-") as tmp:
        m = Material(Path(tmp), pasadizo, upstreams[0][1])
        # REALITY's handshake target, a Go TLS 1.3 server. An OpenSSL 4.1
        # s_server there fails REALITY's authentication for every pair.
        target = {"log": {"level": "error"}, "outbounds": [{"type": "direct"}],
                  "inbounds": [{"type": "http", "listen": "127.0.0.1", "listen_port": m.reality_target_port,
                                "tls": m.server_tls()}]}
        (m.work / "reality-target.json").write_text(json.dumps(target))
        reality_target = subprocess.Popen([upstreams[0][1], "run", "-c", str(m.work / "reality-target.json")],
                                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            pairs = [(("pasadizo", pasadizo), ("pasadizo", pasadizo))]
            for upstream in upstreams:
                pairs += [(("pasadizo", pasadizo), upstream), (upstream, ("pasadizo", pasadizo))]
            for case, build in cases.items():
                if not re.search(args.only, case):
                    continue
                for server, client in pairs:
                    tcp, udp = run_pair(m, build, server, client, pasadizo,
                                        http_server.server_address[1], dns.getsockname()[1])
                    ok = tcp == "ok" and udp == "ok"
                    failed += not ok
                    print(f"{'ok  ' if ok else 'FAIL'} {case:32s} server={server[0]:10s} client={client[0]:10s}"
                          f" tcp={tcp} udp={udp}", flush=True)
        finally:
            reality_target.terminate()
            reality_target.wait()
    print(f"{failed} failed" if failed else "all pairs passed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
