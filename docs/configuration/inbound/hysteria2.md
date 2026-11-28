---
icon: material/alert-decagram
---

!!! quote "Changes in Pasadizo"

    :material-plus: [careful_resume](#careful_resume)

!!! quote "Changes in sing-box 1.14.0"

    :material-plus: [bbr_profile](#bbr_profile)  
    :material-plus: [realm](#realm)  
    :material-alert: [obfs](#obfstype)

!!! quote "Changes in sing-box 1.11.0"

    :material-alert: [masquerade](#masquerade)  
    :material-alert: [ignore_client_bandwidth](#ignore_client_bandwidth)

### Structure

```json
{
  "type": "hysteria2",
  "tag": "hy2-in",
  
  ... // Listen Fields

  "up_mbps": 100,
  "down_mbps": 100,
  "obfs": {
    "type": "salamander",
    "password": "cry_me_a_r1ver"
  },
  "users": [
    {
      "name": "tobyxdd",
      "password": "goofy_ahh_password"
    }
  ],
  "ignore_client_bandwidth": false,
  "tls": {},

  ... // QUIC Fields

  "masquerade": "", // or {}
  "bbr_profile": "",
  "brutal_debug": false,
  "careful_resume": {
    "enabled": false,
    "lifetime": "",
    "state_file": ""
  },
  "realm": {
    "server_url": "https://realm.example.com",
    "token": "",
    "realm_id": "",
    "stun_servers": [],
    "stun_domain_resolver": "", // or {}
    "ip_version": 0,
    "port_mapping": {
      "enabled": false,
      "timeout": "",
      "lifetime": ""
    },
    "http_client": {}
  }
}
```

!!! warning "Difference from official Hysteria2"

    The official program supports an authentication method called **userpass**,
    which essentially uses a combination of `<username>:<password>` as the actual password,
    while sing-box does not provide this alias.
    To use sing-box with the official program, you need to fill in that combination as the actual password.

### Listen Fields

See [Listen Fields](/configuration/shared/listen/) for details.

### Fields

#### up_mbps, down_mbps

Max bandwidth, in Mbps.

Not limited if empty.

Conflict with `ignore_client_bandwidth`.

#### obfs.type

QUIC traffic obfuscator type, one of `salamander` `gecko`.

Disabled if empty.

#### obfs.password

QUIC traffic obfuscator password.

#### obfs.min_packet_size

!!! question "Since sing-box 1.14.0"

Minimum on-wire packet size in bytes. Gecko only.

`512` is used by default.

#### obfs.max_packet_size

!!! question "Since sing-box 1.14.0"

Maximum on-wire packet size in bytes. Gecko only.

`1200` is used by default.

#### users

Hysteria2 users

#### users.password

Authentication password

#### ignore_client_bandwidth

*When `up_mbps` and `down_mbps` are not set*:

Commands clients to use the BBR CC instead of Hysteria CC.

*When `up_mbps` and `down_mbps` are set*:

Deny clients to use the BBR CC.

#### tls

==Required==

TLS configuration, see [TLS](/configuration/shared/tls/#inbound).

### QUIC Fields

See [QUIC Fields](/configuration/shared/quic/) for details.

#### masquerade

HTTP3 server behavior (URL string configuration) when authentication fails.

| Scheme       | Example                 | Description        |
|--------------|-------------------------|--------------------|
| `file`       | `file:///var/www`       | As a file server   |
| `http/https` | `http://127.0.0.1:8080` | As a reverse proxy |

Conflict with `masquerade.type`.

A 404 page will be returned if masquerade is not configured.

#### masquerade.type

HTTP3 server behavior (Object configuration) when authentication fails.

| Type     | Description                 | Fields                              |
|----------|-----------------------------|-------------------------------------|
| `file`   | As a file server            | `directory`                         |
| `proxy`  | As a reverse proxy          | `url`, `rewrite_host`               |
| `string` | Reply with a fixed response | `status_code`, `headers`, `content` |

Conflict with `masquerade`.

A 404 page will be returned if masquerade is not configured.

#### masquerade.directory

File server root directory.

#### masquerade.url

Reverse proxy target URL.

#### masquerade.rewrite_host

Rewrite the `Host` header to the target URL.

#### masquerade.status_code

Fixed response status code.

#### masquerade.headers

Fixed response headers.

#### masquerade.content

Fixed response content.

#### bbr_profile

!!! question "Since sing-box 1.14.0"

BBR congestion control algorithm profile, one of `conservative` `standard` `aggressive`.

`standard` is used by default.

#### brutal_debug

Enable debug information logging for Hysteria Brutal CC.

#### careful_resume

!!! question "Pasadizo only"

Careful Resume ([RFC 9959](https://www.rfc-editor.org/rfc/rfc9959)) for the BBR congestion control. A new QUIC connection normally starts BBR from slow start, which on a path of 100 ms and 200 Mbit/s takes about two seconds to reach full speed. With Careful Resume, Pasadizo saves the bottleneck bandwidth and the minimum round trip time that BBR measures to each peer IP address (the /64 of an IPv6 address), for at most 4096 peers, and a new connection to the same address starts from half of that bandwidth, once the path is confirmed, instead of from slow start.

The connection goes through the phases of the RFC. It first sends as plain BBR until the initial window is acknowledged, and uses the saved values only when the round trip time is more than half and at most ten times the saved one. Then, for at most one round trip, it sends at half the saved bandwidth. If that meets congestion, the saved values are deleted and the connection falls back to half of what was delivered. Loss counts as congestion only when at least 8 packets and more than 5 % of the bytes were lost, because the paths this is for lose a few per cent at random. Only one connection at a time resumes from one set of saved values.

It applies to the data this inbound sends to its clients (their downloads) over BBR: a client that does not set `down_mbps`, or every client with `ignore_client_bandwidth`. A client with `down_mbps` gets Brutal, which has no slow start, and is not changed.

Measured with netem at 200 Mbit/s, 100 ms and 1 % loss, a new connection after a download carried 12.1 MB in its first second instead of 6.7 MB, and reached 90 % of full speed in 0.9 s instead of 1.6 s.

Set `log.level` to `debug` to see each phase change.

#### careful_resume.enabled

Enable Careful Resume.

#### careful_resume.lifetime

How long saved values may be reused after they were last measured.

`10m` is used by default.

#### careful_resume.state_file

A file that keeps the saved values across restarts. It is read at start and written every 10 seconds after a change and at stop. A file that cannot be read or written is logged as a warning and does not stop Pasadizo; values saved with a time in the future count as expired. Every inbound and outbound needs a file of its own.

Not kept if empty.

#### realm

!!! question "Since sing-box 1.14.0"

Register this inbound to a Hysteria Realm rendezvous service to enable NAT traversal.

See [Hysteria Realm](/configuration/service/hysteria-realm/) for the rendezvous service.

#### realm.server_url

==Required==

Realm rendezvous service URL.

#### realm.token

Bearer token for the realm. Must match one of `users[].token` configured on the realm.

#### realm.realm_id

==Required==

Slot identifier on the realm.

1–64 characters, must match `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`.

Outbounds must use the same `realm_id` to find this server.

#### realm.stun_servers

==Required==

List of STUN servers (`host` or `host:port`) used to discover public addresses.

#### realm.stun_domain_resolver

Set domain resolver to use for resolving STUN server domain names.

This option uses the same format as the [route DNS rule action](/configuration/dns/rule_action/#route) without the `action` field.

Setting this option directly to a string is equivalent to setting `server` of this options.

If empty, the default domain resolver is used.

#### realm.ip_version

Restrict realm connections (STUN, hole punching, and the resulting QUIC path) to a single IP version.

`4` or `6`. Both are used if empty.

The `listen` address must be compatible with the selected version.

#### realm.port_mapping

Maintain a UDP port mapping on the local gateway via UPnP or NAT-PMP.

Improves hole-punching reliability behind gateways that support it.

Requires IPv4: conflicts with `"ip_version": 6`.

#### realm.port_mapping.enabled

Enable port mapping.

#### realm.port_mapping.timeout

Timeout for gateway discovery and mapping operations.

`10s` is used by default.

#### realm.port_mapping.lifetime

Lease lifetime of the mapping.

`10m` is used by default.

#### realm.http_client

HTTP client used to talk to the realm.

See [HTTP Client](/configuration/shared/http-client/) for details.
