# Pasadizo

Pasadizo ("secret passage" in Spanish) is a proxy platform derived from [sing-box](https://github.com/SagerNet/sing-box). It follows sing-box's `testing` branch and adds features for a home gateway that sends a whole network through tunnels on a lossy, filtered line.

**Pasadizo is not affiliated with sing-box or its authors**, and it is not endorsed by them. Report problems with Pasadizo here, never upstream.

## What Pasadizo adds

| Feature | Configuration | Docs |
| --- | --- | --- |
| `loadbalance` outbound group: spreads connections over its members by consistent hashing of the destination or round-robin, with URL-test health checks | `"type": "loadbalance"` | [docs/configuration/outbound/loadbalance.md](../docs/configuration/outbound/loadbalance.md) |
| `fallback` outbound group: the first healthy member in a fixed order, with `recover_after` before it moves back up | `"type": "fallback"` | [docs/configuration/outbound/fallback.md](../docs/configuration/outbound/fallback.md) |
| Several QUIC connections per Hysteria2 outbound, so one exit is not limited by one connection | `"connections": 4` on a `hysteria2` outbound | [docs/configuration/outbound/hysteria2.md](../docs/configuration/outbound/hysteria2.md) |
| A webhook for outbound group changes (selected member, member health) | `experimental.group_webhook` | [docs/configuration/experimental/group-webhook.md](../docs/configuration/experimental/group-webhook.md) |
| Fair bandwidth sharing between client addresses | route action option `fair_share` | [docs/configuration/route/rule_action.md](../docs/configuration/route/rule_action.md) |
| Traffic per client address in the Clash API | `GET /stats/sources` | [docs/configuration/experimental/clash-api.md](../docs/configuration/experimental/clash-api.md) |
| Prometheus metrics in the Clash API | `GET /metrics` | [docs/configuration/experimental/clash-api.md](../docs/configuration/experimental/clash-api.md) |
| Cloudflare WARP mode for the MASQUE client (incomplete: the QUIC library still refuses the extended CONNECT) | `masque-client` endpoint | [docs/configuration/endpoint/masque-client.md](../docs/configuration/endpoint/masque-client.md) |

Each feature is one commit on top of upstream, so it can be read, reverted or offered upstream on its own.

## Releases

Tags look like `v1.15.0-alpha.14-pasadizo.1`: the upstream version, then Pasadizo's release number. Each release has static linux `amd64` and `arm64` binaries named `pasadizo-<version>-linux-<arch>`, a `SHA256SUMS` file and a GitHub build provenance attestation (`gh attestation verify <file> --repo pasadizo-net/pasadizo`). Binaries are built with upstream's release build tags and linker flags (`release/DEFAULT_BUILD_TAGS_OTHERS`, `release/LDFLAGS`).

## Building

```sh
CGO_ENABLED=0 go build -trimpath -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" \
  -ldflags "$(cat release/LDFLAGS) -s -w -buildid=" -o pasadizo ./cmd/sing-box
```

The Go module path stays `github.com/sagernet/sing-box`, so that upstream merges stay clean.

## Keeping up with upstream

A weekly workflow (`.github/workflows/pasadizo-sync.yml`) merges upstream's `testing` branch into a new branch, builds and tests it, and opens a pull request. A person reads the upstream changes and merges it; nothing reaches `main` automatically. A merge conflict opens an issue instead. History is never rewritten: upstream is merged, not rebased.

## License

Pasadizo is distributed under the same license as sing-box: the GNU General Public License, version 3 or later ([LICENSE](../LICENSE)), with sing-box's additional term that no derivative work may use its name or imply association with it without prior consent. That is why this project has its own name and states above that it is not affiliated with sing-box. Copyright of the upstream code stays with its authors; see the upstream [README](../README.md).
