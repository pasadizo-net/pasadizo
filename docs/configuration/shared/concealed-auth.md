---
icon: material/new-box
---

!!! question "Since sing-box 1.15.0"

# Concealed Authentication

The Concealed HTTP authentication scheme ([RFC 9729](https://www.rfc-editor.org/rfc/rfc9729)) lets an HTTP server hide that it is a proxy. The client signs a value exported from its TLS session with a private key, and sends the key ID, its public key and the signature in an `Authorization: Concealed ...` header field (`Proxy-Authorization` for `CONNECT` and forwarded requests). The server sends no challenge. A request without a valid proof gets the decoy: the answer of the [fallback](#fallback), or a plain `404 Not Found`, the same answer as for a resource that does not exist. An active probe sees a web server, never a `401` or `407`.

A proof is bound to its TLS session and to the scheme, host and port of the request's target, so it cannot be replayed on another connection. It needs TLS 1.3, or TLS 1.2 with the extended master secret. Over HTTP/3 the client sends the proof only after the handshake, never in 0-RTT data, and the server ignores a proof that arrived in 0-RTT data.

It is supported by the [HTTP inbound](/configuration/inbound/http/) and [outbound](/configuration/outbound/http/), and by the [MASQUE server](/configuration/endpoint/masque-server/) and [client](/configuration/endpoint/masque-client/), over HTTP/1.1, HTTP/2 and HTTP/3.

Generate a key pair with:

```shell
pasadizo generate concealed-auth-keypair         # Ed25519
pasadizo generate concealed-auth-keypair p256    # ECDSA P-256
```

`PrivateKey` goes to the client and `PublicKey` to the server. Use a key for this scheme only.

## Inbound

### Structure

```json
{
  "users": [
    {
      "name": "parham",
      "key_id": "house",
      "public_key": "gEyHTN0vVhjoVXysQtQ57-kp2wH05Um1F68S5xPIr1A"
    }
  ],
  "fallback": {
    "server": "127.0.0.1",
    "server_port": 8080
  },
  "frontend_source_ip_cidr": []
}
```

TLS is required on the inbound, unless `frontend_source_ip_cidr` is set.

`users` of the inbound itself still work: a request with valid Basic credentials is accepted too, and a request with wrong ones gets the decoy, never a challenge. `auth_exempt_source_ip_cidr` of the HTTP inbound applies as before.

### Fields

#### users

==Required==

The keys the server accepts.

`name` is the user name in logs and in the `auth_user` route rule; `key_id` is used when it is empty.

`key_id` is the key ID the client sends, as a UTF-8 string. Each one must be unique.

`public_key` is an Ed25519 or ECDSA P-256 public key: base64 of the encoding in the RFC (32 bytes for Ed25519, a 65-byte uncompressed point for P-256) as printed by the generator, base64 of the DER `SubjectPublicKeyInfo`, or a PEM `PUBLIC KEY` block. Standard and URL-safe base64 are accepted, with or without padding.

#### fallback

A plain HTTP server, such as a web server on the same host, that answers every request without a valid proof.

Over HTTP/1.1 the connection is relayed to it byte for byte from the first such request on, so the fallback answers that request and every later one on the connection. Over HTTP/2 and HTTP/3 each such request is forwarded to it as an HTTP/1.1 request with the original `Host`.

A plain `404 Not Found` is sent if empty.

#### frontend_source_ip_cidr

Source address prefixes trusted to send the `Concealed-Auth-Export` header field (RFC 9729, Section 6.2): a frontend that terminates TLS computes the exporter output and passes it with the request. The header field is ignored from every other source.

The prefixes match the address of the connection, never an address from a forwarding header. The server strips the header field from every request it forwards.

## Outbound

### Structure

```json
{
  "key_id": "house",
  "private_key": "MC4CAQAwBQYDK2VwBCIEILoeVQfoLJ03VcM2eskAHWh2CBxSXuEbaRad0lUFvPr3",
  "realm": ""
}
```

TLS is required on the outbound, and `username` must be empty.

### Fields

#### key_id

==Required==

The key ID, as a UTF-8 string.

#### private_key

==Required==

An Ed25519 or ECDSA P-256 private key: base64 of the DER PKCS #8 form as printed by the generator, base64 of a 32-byte Ed25519 seed, or a PEM `PRIVATE KEY` or `EC PRIVATE KEY` block.

#### realm

The realm sent in the `realm` parameter and bound into the proof. Not sent if empty.
