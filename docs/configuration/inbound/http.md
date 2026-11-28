### Structure

```json
{
  "type": "http",
  "tag": "http-in",
  
  ... // Listen Fields
  
  "version": [],
  "users": [
    {
      "username": "admin",
      "password": "admin"
    }
  ],
  "auth_exempt_source_ip_cidr": [],
  "concealed_auth": {},
  "tls": {},
  "set_system_proxy": false,

  ... // HTTP2 Fields / QUIC Fields
}
```

### Listen Fields

See [Listen Fields](/configuration/shared/listen/) for details.

### Fields

#### version

!!! question "Since sing-box 1.15.0"

List of HTTP versions to serve.

Available values: `1`, `2`, `3`.

`1` and `2` are used by default.

TLS is required for `3`.

#### tls

TLS configuration, see [TLS](/configuration/shared/tls/#inbound).

#### users

HTTP users.

No authentication if empty.

#### auth_exempt_source_ip_cidr

!!! question "Pasadizo only"

Source address prefixes whose connections need no credentials, for example `["10.0.0.0/8", "127.0.0.0/8"]`. Every other source must authenticate as one of `users`.

The prefixes match the address of the connection, never an address from a forwarding header such as `X-Forwarded-For`, which a client can set.

No authentication required if empty.

#### concealed_auth

!!! question "Pasadizo only"

[Concealed authentication](/configuration/shared/concealed-auth/#inbound) (RFC 9729): clients prove a key bound to their TLS session, and every request without a valid proof gets a decoy answer instead of a `407`, so an active probe sees a web server.

#### set_system_proxy

!!! quote ""

    Only supported on Linux, Android, Windows, and macOS.

!!! warning ""

    To work on Android and Apple platforms without privileges, use tun.platform.http_proxy instead.

Automatically set system proxy configuration when start and clean up when stop.

### HTTP2 Fields

!!! question "Since sing-box 1.15.0"

When `version` contains `2`.

See [HTTP2 Fields](/configuration/shared/http2/) for details.

### QUIC Fields

!!! question "Since sing-box 1.15.0"

When `version` contains `3`, [HTTP2 Fields](#http2-fields) are replaced by QUIC Fields.

See [QUIC Fields](/configuration/shared/quic/) for details.
