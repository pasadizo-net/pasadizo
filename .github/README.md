<p align="center">
  <img src="https://raw.githubusercontent.com/pasadizo-net/.github/main/assets/banner.png" alt="Pasadizo, a secret passage for your whole network" width="100%">
</p>

<p align="center">
  <a href="https://github.com/pasadizo-net/pasadizo/releases"><img alt="Release" src="https://img.shields.io/github/v/release/pasadizo-net/pasadizo?include_prereleases&sort=semver&label=release&color=FFB347"></a>
  <a href="https://github.com/pasadizo-net/pasadizo/actions/workflows/pasadizo-ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/pasadizo-net/pasadizo/pasadizo-ci.yml?branch=main&label=ci"></a>
  <a href="https://github.com/pasadizo-net/pasadizo/actions/workflows/pasadizo-sync.yml"><img alt="Upstream sync" src="https://img.shields.io/github/actions/workflow/status/pasadizo-net/pasadizo/pasadizo-sync.yml?branch=main&label=upstream%20sync"></a>
  <a href="../LICENSE"><img alt="License: GPL-3.0-or-later" src="https://img.shields.io/badge/license-GPL--3.0--or--later-1E1B4B"></a>
  <a href="../go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/pasadizo-net/pasadizo?color=00ADD8"></a>
  <a href="https://github.com/SagerNet/sing-box"><img alt="Based on sing-box" src="https://img.shields.io/badge/based%20on-sing--box-64748B"></a>
</p>

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

Tags look like `v1.15.0-alpha.14-pasadizo.2`: the upstream version, then Pasadizo's release number. GoReleaser (`.goreleaser.yaml`, run by `.github/workflows/pasadizo-release.yml`) builds each release with upstream's release build tags and linker flags (`release/DEFAULT_BUILD_TAGS_OTHERS`, `release/LDFLAGS`). A release has:

- static binaries for linux `amd64`, `arm64` and `armv7` and darwin `amd64` and `arm64`, in `pasadizo-<version>-<os>-<arch>.tar.gz` archives with the license, this README, an example configuration and the systemd units;
- `.deb` and `.rpm` packages for linux;
- a `checksums.txt` file and a GitHub build provenance attestation for every file in it (`gh attestation verify <file> --repo pasadizo-net/pasadizo`);
- the Homebrew cask `pasadizo` in [pasadizo-net/homebrew-tap](https://github.com/pasadizo-net/homebrew-tap) and the AUR package [`pasadizo-bin`](https://aur.archlinux.org/packages/pasadizo-bin).

GoReleaser pushes the cask and the package over SSH. The Actions secret `HOMEBREW_TAP_KEY` holds the private half of a deploy key with write access on `pasadizo-net/homebrew-tap`, and `AUR_KEY` holds the private half of an SSH key registered on the AUR account that maintains `pasadizo-bin`. When a secret is not set, the release skips that step and publishes the rest.

## Installation

Pasadizo installs beside sing-box and does not replace it: the program is `pasadizo`, the configuration is in `/etc/pasadizo`, and the systemd units are `pasadizo.service` and `pasadizo@.service`.

| From | Command |
| --- | --- |
| A GitHub release | Download the archive for your system from the [releases](https://github.com/pasadizo-net/pasadizo/releases), check it with `sha256sum --ignore-missing -c checksums.txt`, and put `pasadizo` in your `PATH`. |
| Debian, Ubuntu | `sudo apt install ./pasadizo_<version>_<arch>.deb` |
| Fedora, RHEL | `sudo dnf install ./pasadizo-<version>.<arch>.rpm` |
| Homebrew (macOS, Linux) | `brew install --cask pasadizo-net/tap/pasadizo` |
| Arch Linux (AUR) | `yay -S pasadizo-bin`, or `git clone https://aur.archlinux.org/pasadizo-bin.git && cd pasadizo-bin && makepkg -si` |

The packages install `/usr/bin/pasadizo`, an example `/etc/pasadizo/config.json` (a local `mixed` proxy on `127.0.0.1:2080`; an upgrade keeps your changes), `pasadizo.service`, which runs `pasadizo -D /var/lib/pasadizo -C /etc/pasadizo run` (every `.json` file in `/etc/pasadizo`), and `pasadizo@.service`, which runs one configuration `/etc/pasadizo/<name>.json` with its own state in `/var/lib/pasadizo-<name>`. Do not use both units with the same directory: `pasadizo.service` also reads the `<name>.json` files of `pasadizo@.service`. Start it with `sudo systemctl enable --now pasadizo`.

## Building

```sh
CGO_ENABLED=0 go build -trimpath -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" \
  -ldflags "$(cat release/LDFLAGS) -s -w -buildid=" -o pasadizo ./cmd/pasadizo
```

The Go module is `github.com/pasadizo-net/pasadizo`, and the main package is `./cmd/pasadizo`. Set the version with `-X github.com/pasadizo-net/pasadizo/constant.Version=<version>` in `-ldflags`. To build the release files locally, run `PASADIZO_TAGS="$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" PASADIZO_LDFLAGS="$(cat release/LDFLAGS)" goreleaser release --snapshot --clean --skip=publish`; they are in `dist/`.

## The rename

Pasadizo changes the Go module path from `github.com/sagernet/sing-box` to `github.com/pasadizo-net/pasadizo`, moves `cmd/sing-box` to `cmd/pasadizo`, and names the program `pasadizo` in its help and `version` output. One script does all of it: [`.github/pasadizo/rename.sh`](pasadizo/rename.sh). It changes only build references (imports, `go.mod`, linker flags, `go_package`, `Makefile`, `Dockerfile`, `release/`), runs `gofmt` on the Go files it changed, and leaves other sagernet modules, URLs, `docs/`, `README.md` and `LICENSE` as they are. It is idempotent, and CI runs it to check that the tree stays renamed. Code for Pasadizo imports `github.com/pasadizo-net/pasadizo/...`.

## Keeping up with upstream

A weekly workflow (`.github/workflows/pasadizo-sync.yml`) merges upstream's `testing` branch into a new branch, builds and tests it, and opens a pull request. A person reads the upstream changes and merges it; nothing reaches `main` automatically. A merge conflict opens an issue instead. History is never rewritten: upstream is merged, not rebased.

Upstream is not merged as it is, because a plain merge conflicts wherever upstream changes a line next to a renamed line, for example an import added beside a `sing-box` import. [`.github/pasadizo/sync-merge.sh`](pasadizo/sync-merge.sh) does the merge instead:

1. It runs the same `rename.sh` on upstream's new head in a temporary branch and commits it as "Rename upstream `<sha>` to Pasadizo". It does the same for the last upstream commit that `main` contains.
2. It merges the renamed upstream head into the sync branch, with the renamed last upstream commit as the merge base. The rename is identical on all three sides, so only upstream's real changes and Pasadizo's own changes take part in the merge.
3. The merge commit has the renamed upstream head as its second parent, and the parent of that commit is upstream's own commit, so the next sync finds it as its base.

To do a sync by hand, run `.github/pasadizo/sync-merge.sh upstream/testing` on a new branch after `git fetch upstream testing`.

## License

Pasadizo is distributed under the same license as sing-box: the GNU General Public License, version 3 or later ([LICENSE](../LICENSE)), with sing-box's additional term that no derivative work may use its name or imply association with it without prior consent. That is why this project has its own name and states above that it is not affiliated with sing-box. Copyright of the upstream code stays with its authors; see the upstream [README](../README.md).
