#!/usr/bin/env bash
# Pasadizo builds with its own sing-quic (github.com/pasadizo-net/sing-quic),
# through the `replace` line in go.mod. The `require` line still names the
# commit that sing-box pins, and an upstream sync moves it. When the replace
# is not moved with it, the build passes on OLDER QUIC code than sing-box asks
# for, and nothing else would fail. This check fails then.
#
# It passes when the commit of the require line is an ancestor of the commit
# of the replace line in pasadizo-net/sing-quic. To fix a failure: merge the
# open sync pull request of pasadizo-net/sing-quic (or run its pasadizo-sync
# workflow), then point the replace at the new commit of its main:
#
#   go mod edit -replace github.com/sagernet/sing-quic=github.com/pasadizo-net/sing-quic@<version>
#
# where <version> is `TZ=UTC git log -1 --date=format-local:%Y%m%d%H%M%S
# --format=v0.0.0-%cd-%h --abbrev=12 origin/main` in a clone of it.
set -euo pipefail

cd "$(dirname "$0")/../.."
module=github.com/sagernet/sing-quic
ours=https://github.com/pasadizo-net/sing-quic.git
upstream=https://github.com/SagerNet/sing-quic.git

required="$(go mod edit -json | jq -r --arg m "$module" '.Require[] | select(.Path == $m) | .Version')"
replaced="$(go mod edit -json | jq -r --arg m "$module" '.Replace[]? | select(.Old.Path == $m) | "\(.New.Path) \(.New.Version)"')"
if [ -z "$replaced" ]; then
    echo "go.mod has no replace of $module: Pasadizo would build with upstream's sing-quic" >&2
    exit 1
fi
read -r replaced_path replaced_version <<<"$replaced"
if [ "$replaced_path" != github.com/pasadizo-net/sing-quic ]; then
    echo "go.mod replaces $module with $replaced_path, not with github.com/pasadizo-net/sing-quic" >&2
    exit 1
fi

# The commit of a pseudo-version is its last 12 characters; any other version
# is a tag.
revision() {
    if [[ "$1" =~ [.-][0-9]{14}-([0-9a-f]{12})$ ]]; then
        echo "${BASH_REMATCH[1]}"
    else
        echo "refs/tags/$1"
    fi
}

clone="$(mktemp -d)"
trap 'rm -rf "$clone"' EXIT
git clone --quiet --bare --filter=blob:none "$ours" "$clone"
# upstream's tags, for a require line that names a release
git -C "$clone" fetch --quiet --filter=blob:none "$upstream" '+refs/tags/*:refs/tags/*' || true

required_commit="$(git -C "$clone" rev-parse --verify --quiet "$(revision "$required")^{commit}")" || {
    echo "the commit sing-box pins ($required) is not in pasadizo-net/sing-quic: merge its upstream sync first" >&2
    exit 1
}
replaced_commit="$(git -C "$clone" rev-parse --verify --quiet "$(revision "$replaced_version")^{commit}")" || {
    echo "the replace names $replaced_version, which is not a commit of pasadizo-net/sing-quic" >&2
    exit 1
}
if ! git -C "$clone" merge-base --is-ancestor "$replaced_commit" refs/heads/main; then
    echo "the replace names ${replaced_commit:0:12}, which is not on main of pasadizo-net/sing-quic" >&2
    exit 1
fi
if ! git -C "$clone" merge-base --is-ancestor "$required_commit" "$replaced_commit"; then
    echo "the replace (${replaced_commit:0:12}) does not contain the commit sing-box pins (${required_commit:0:12}): move the replace to a newer commit of pasadizo-net/sing-quic main" >&2
    exit 1
fi
echo "sing-quic: the replace ${replaced_commit:0:12} contains the pinned ${required_commit:0:12}"
