---
icon: material/new-box
---

!!! question "Since sing-box 1.15.0"

### Structure

```json
{
  "type": "gost",
  "tag": "gost-out",

  "server": "127.0.0.1",
  "server_port": 8443,
  "username": "",
  "password": "",
  "transport": "",
  "connector": "",
  "tls": {},
  "kcp": {
    "key": "",
    "crypt": "",
    "mode": "",
    "mtu": 0,
    "send_window": 0,
    "receive_window": 0,
    "data_shard": 0,
    "parity_shard": 0,
    "socket_buffer": 0,
    "no_compression": false
  },
  "icmp": {
    "keep_alive_period": "",
    "idle_timeout": ""
  },
  "mux": {
    "version": 1,
    "keep_alive_disabled": false,
    "keep_alive_interval": "",
    "keep_alive_timeout": "",
    "max_frame_size": 0,
    "max_receive_buffer": 0,
    "max_stream_buffer": 0,
    "open_timeout": "",
    "stall_timeout": ""
  },

  ... // Dial Fields
}
```

A node of a [gost](https://github.com/go-gost/gost) v3 chain. It is the client side of a gost service with an `http` or `relay` handler on an `mtls`, `mtcp`, `kcp` or `icmp` listener, for example:

```yaml
services:
  - name: service-0
    addr: :8443
    handler:
      type: http
      auth:
        username: user
        password: pass
    listener:
      type: mtls
```

```yaml
services:
  - name: relay-kcp
    addr: :51820
    handler:
      type: relay
      auth:
        username: user
        password: pass
    listener:
      type: kcp
      metadata:
        kcp.crypt: aes
        kcp.key: secret
```

The outbound keeps one session to the server and opens one stream per connection. The session is [smux](https://github.com/xtaci/smux) over TCP (TLS when `tls` is enabled), smux over KCP, or QUIC over ICMP echo, as set by `transport`. On each stream it sends the request of the `connector` for the destination: an HTTP `CONNECT`, as gost's `http` connector does, or a relay `CONNECT`, as gost's `relay` connector does. Only TCP is supported.

A session that is closed, or that cannot open a stream within `open_timeout`, is replaced by a new one on the next connection. Concurrent connections share the dial of a new session.

A relay handler sends its answer together with the first bytes from the destination, so a relay `CONNECT` that fails ends the connection at its first read, and outbound groups do not see it as a failed dial.

When the server answers an HTTP `CONNECT` with `403`, `502`, `503` or `504`, the server works but could not reach the destination; gost answers `503` when its own dial fails. Outbound groups (`urltest`, `fallback`, `loadbalance`) do not fail over on this error. Any other status, for example `407` or the probe resistance code on wrong credentials, is a failure of the outbound.

Chain gost nodes with `detour`: the outbound of the next node sets `detour` to the outbound of the previous one, as hops of a gost chain.

### Fields

#### server

==Required==

The server address.

#### server_port

==Required== unless `transport` is `icmp`.

The server port.

#### username

Basic authorization username.

#### password

Basic authorization password.

#### transport

The listener of the gost service:

| Transport | Listener                                                                                                    |
|-----------|-------------------------------------------------------------------------------------------------------------|
| `tcp`     | `mtls` when `tls` is enabled, `mtcp` when it is not. The default.                                           |
| `kcp`     | `kcp`. smux runs over a [KCP](https://github.com/xtaci/kcp-go) session over UDP. See [kcp](#kcp).          |
| `icmp`    | `icmp`. QUIC runs over ICMP echo requests. See [icmp](#icmp).                                               |

The `icmp` transport opens a raw ICMP socket, which needs `CAP_NET_RAW` or root, sends from the address of the default route and ignores the dial fields. `server` must be an IPv4 address. It needs a build with QUIC.

#### connector

The handler of the gost service: `http` (the default) or `relay`.

#### tls

TLS configuration, see [TLS](/configuration/shared/tls/#outbound). For the `tcp` transport, enable it for an `mtls` listener and leave it disabled for `mtcp`. For the `icmp` transport, it configures QUIC; when it is disabled, the certificate of the server is not verified and the ALPN is `h3` and `quic/v1`, as in gost. The `kcp` transport ignores it.

gost generates a self-signed certificate when its configuration has none, so set `insecure` or `certificate` for such a server.

#### kcp

The KCP session, for the `kcp` transport. The fields match the `kcp.*` metadata of gost's `kcp` dialer, and empty fields use gost's defaults. `key`, `crypt`, `data_shard`, `parity_shard` and `no_compression` must match the server.

| Field            | gost metadata    | Default            |
|------------------|------------------|--------------------|
| `key`            | `kcp.key`        | `it's a secrect`   |
| `crypt`          | `kcp.crypt`      | `aes`              |
| `mode`           | `kcp.mode`       | `fast`             |
| `mtu`            | `kcp.mtu`        | `1350`             |
| `send_window`    | `kcp.sndwnd`     | `1024`             |
| `receive_window` | `kcp.rcvwnd`     | `1024`             |
| `data_shard`     | `kcp.config` `datashard`   | `10`               |
| `parity_shard`   | `kcp.config` `parityshard` | `3`                |
| `socket_buffer`  | `kcp.config` `sockbuf`     | `4194304`          |
| `no_compression` | `kcp.nocomp`     | `false`            |

`crypt` is one of `aes`, `aes-128`, `aes-192`, `salsa20`, `blowfish`, `twofish`, `cast5`, `3des`, `tea`, `xtea`, `xor`, `sm4` and `none`. The key is derived from `key` with PBKDF2, as in gost and kcptun. `mode` is one of `normal`, `fast`, `fast2` and `fast3`. Without `no_compression`, the session is compressed with snappy.

The smux session over KCP takes its settings from [mux](#mux), with gost's KCP defaults: `max_receive_buffer` is `4194304` and `max_stream_buffer` is `2097152`. Set `mux.version`, `mux.max_receive_buffer` and `mux.max_stream_buffer` to the `kcp.smuxver`, `kcp.smuxbuf` and `kcp.streambuf` of the server, and `mux.keep_alive_interval` to its `kcp.keepalive`.

#### icmp

The QUIC connection, for the `icmp` transport.

##### keep_alive_period

How often to send a QUIC keepalive. `10s` will be used if empty, as in gost.

##### idle_timeout

Close the connection when nothing has arrived for this long. `90s` will be used if empty, as in gost.

`mux.open_timeout` limits the QUIC handshake. The other `mux` fields do not apply.

#### mux

The smux session of the `tcp` and `kcp` transports. The fields match the `mux.*` metadata of gost's `mtls` dialer, and empty fields use gost's defaults.

##### version

The smux protocol version, `1` or `2`. It must match the server. `1` will be used if empty, as in gost.

##### keep_alive_disabled

Disable smux keepalive.

##### keep_alive_interval

How often to send a keepalive frame. `10s` will be used if empty.

##### keep_alive_timeout

Close the session when nothing has arrived for this long. `30s` will be used if empty.

The defaults keep a dead session for up to 30 seconds, and every connection on it waits until then. `3s` and `10s` find a dead path sooner; keep the timeout at least three times the interval, and above the keepalive interval of the server, `10s` by default.

##### max_frame_size

The largest frame to send. `32768` will be used if empty.

##### max_receive_buffer

The receive buffer of the session in bytes. `4194304` will be used if empty.

##### max_stream_buffer

The receive buffer of one stream in bytes. `65536` will be used if empty.

##### open_timeout

The time to dial a session, to open a stream and to read the answer to `CONNECT`. `15s` will be used if empty. The deadline of the connection applies when it is shorter.

##### stall_timeout

Close the session when one write to the server does not complete within this time. smux keepalive only watches what arrives, so a path that still delivers the server's frames but no longer takes ours would hold every stream otherwise. `30s` will be used if empty.

### Dial Fields

See [Dial Fields](/configuration/shared/dial/) for details.
