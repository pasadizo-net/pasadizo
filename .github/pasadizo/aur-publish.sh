#!/usr/bin/env bash
# Publishes the AUR package pasadizo-bin from the PKGBUILD and .SRCINFO that
# GoReleaser wrote to dist/aur/ (`aurs` has `skip_upload: true`), after adding
# `options=('!strip')` to both. GoReleaser cannot write an `options` line, and
# without it makepkg strips the binary again, so the installed
# /usr/bin/pasadizo differed from the attested binary in the release archive
# (2,363 bytes smaller in .10, from the same verified archive).
#
# Usage: aur-publish.sh <tag>. Needs AUR_KEY, the private half of the SSH key
# registered on the package maintainer's AUR account. Run from the
# repository's root after GoReleaser.
set -euo pipefail

tag=${1:?usage: aur-publish.sh <tag>}
name=pasadizo-bin
pkgbuild=dist/aur/${name}.pkgbuild
srcinfo=dist/aur/${name}.srcinfo
: "${AUR_KEY:?AUR_KEY is not set}"

for f in "$pkgbuild" "$srcinfo"; do
	[ -s "$f" ] || {
		echo "$f is missing: did GoReleaser run with the aur pipe?" >&2
		exit 1
	}
done

# The AUR's ed25519 host key, fingerprint
# SHA256:RFzBCUItH9LZS0cKB5UE6ceAYhBD5C8GeOBip8Z11+4 as the Arch wiki lists it.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf '%s\n' "aur.archlinux.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEuBKrPzbawxA/k2g6NcyV5jmqwJ2s+zpgZGZ7tpLIcN" >"$work/known_hosts"
install -m 600 /dev/null "$work/key"
printf '%s\n' "$AUR_KEY" >"$work/key"
export GIT_SSH_COMMAND="ssh -i $work/key -o IdentitiesOnly=yes -o UserKnownHostsFile=$work/known_hosts -o StrictHostKeyChecking=yes"

git clone -q "ssh://aur@aur.archlinux.org/${name}.git" "$work/repo"

# PKGBUILD: the line after `backup=(...)`. .SRCINFO: the line after the
# `backup = ...` line of the pkgbase section.
awk '{ print } /^backup=\(/ && !done { print "options=('"'"'!strip'"'"')"; done = 1 } END { if (!done) exit 1 }' \
	"$pkgbuild" >"$work/repo/PKGBUILD"
awk '{ print } /^\tbackup = / && !done { print "\toptions = !strip"; done = 1 } END { if (!done) exit 1 }' \
	"$srcinfo" >"$work/repo/.SRCINFO"

cd "$work/repo"
git add PKGBUILD .SRCINFO
if git diff --cached --quiet; then
	echo "${name} is already at ${tag}"
	exit 0
fi
git -c user.name="Parham Alvani" -c user.email="parham.alvani@gmail.com" \
	commit -q -m "pasadizo ${tag}"
git push -q origin HEAD:master
echo "published ${name} ${tag} with options=('!strip')"
