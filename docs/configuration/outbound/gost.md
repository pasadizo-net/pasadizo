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
  "tls": {},
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

A node of a [gost](https://github.com/go-gost/gost) v3 chain whose service has an `http` handler on an `mtls` listener, or on an `mtcp` listener when TLS is disabled. It is the client side of this gost service:

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

The outbound keeps one [smux](https://github.com/xtaci/smux) session to the server, over TLS when `tls` is enabled, and opens one stream per connection. On each stream it sends an HTTP `CONNECT` request for the destination, as gost's `http` connector does. Only TCP is supported.

A session that is closed, or that cannot open a stream within `open_timeout`, is replaced by a new one on the next connection. Concurrent connections share the dial of a new session.

When the server answers the `CONNECT` with `403`, `502`, `503` or `504`, the server works but could not reach the destination; gost answers `503` when its own dial fails. Outbound groups (`urltest`, `fallback`, `loadbalance`) do not fail over on this error. Any other status, for example `407` or the probe resistance code on wrong credentials, is a failure of the outbound.

Chain gost nodes with `detour`: the outbound of the next node sets `detour` to the outbound of the previous one, as hops of a gost chain.

### Fields

#### server

==Required==

The server address.

#### server_port

==Required==

The server port.

#### username

Basic authorization username.

#### password

Basic authorization password.

#### tls

TLS configuration, see [TLS](/configuration/shared/tls/#outbound). Enable it for an `mtls` listener and leave it disabled for `mtcp`.

gost generates a self-signed certificate when its configuration has none, so set `insecure` or `certificate` for such a server.

#### mux

The smux session. The fields match the `mux.*` metadata of gost's `mtls` dialer, and empty fields use gost's defaults.

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
