#!/usr/bin/env bash
# Merge an upstream sing-box commit into the current Pasadizo branch.
#
# Usage: sync-merge.sh <upstream-ref> [conflicts-file]
#
# Pasadizo carries the rename of .github/pasadizo/rename.sh, so a plain merge
# of upstream conflicts at every line that upstream changes next to a renamed
# line (an import added beside a sing-box import, for example). This script
# merges renamed trees instead:
#
#   1. base: the last upstream commit that is in HEAD (git merge-base).
#   2. Run the same rename.sh on base and on the new upstream commit, and
#      commit each on top of its upstream commit ("Rename upstream <sha> to
#      Pasadizo").
#   3. Three-way merge HEAD and the renamed upstream, with the renamed base as
#      the merge base. The rename is the same on all three sides, so only
#      upstream's real changes and Pasadizo's own changes take part.
#   4. Commit the merge with two parents: HEAD and the renamed upstream. The
#      parent of the renamed upstream is the upstream commit, so the next run
#      finds it as its base.
#
# On a conflict nothing is committed, the conflicting paths go to
# conflicts-file (default conflicts.txt), and the script exits 1.
set -euo pipefail

upstream_ref="${1:?usage: sync-merge.sh <upstream-ref> [conflicts-file]}"
conflicts_file="${2:-conflicts.txt}"

root="$(git rev-parse --show-toplevel)"
cd "$root"
upstream="$(git rev-parse --verify "$upstream_ref^{commit}")"
base="$(git merge-base HEAD "$upstream")"

work="$(mktemp -d)"
cleanup() {
	git worktree remove --force "$work/tree" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
# Use this branch's rename.sh for both sides; upstream does not have it.
cp .github/pasadizo/rename.sh "$work/rename.sh"

# renamed COMMIT: print the commit of COMMIT renamed to Pasadizo.
renamed() {
	local commit="$1"
	git worktree add --quiet --detach "$work/tree" "$commit"
	(
		cd "$work/tree"
		bash "$work/rename.sh"
		git add -A
		git commit --quiet --no-verify --no-gpg-sign -m "Rename upstream ${commit:0:8} to Pasadizo" -m "Made by .github/pasadizo/rename.sh."
		git rev-parse HEAD
	)
	git worktree remove --force "$work/tree"
}

renamed_base="$(renamed "$base")"
renamed_upstream="$(renamed "$upstream")"

if ! result="$(git merge-tree --write-tree --name-only --merge-base="$renamed_base" HEAD "$renamed_upstream")"; then
	# The first line is the tree, then the conflicting paths up to a blank line.
	printf '%s\n' "$result" | sed -n '2,/^$/p' | sed '/^$/d' | sort -u >"$conflicts_file"
	echo "sync-merge.sh: merging upstream ${upstream:0:8} conflicts in:" >&2
	cat "$conflicts_file" >&2
	exit 1
fi
tree="$(printf '%s\n' "$result" | head -n 1)"
merge="$(git commit-tree "$tree" -p HEAD -p "$renamed_upstream" -m "Merge upstream ${upstream:0:8} (renamed to Pasadizo)" -m "Upstream ${upstream}, renamed by .github/pasadizo/rename.sh in ${renamed_upstream:0:8}.")"
git merge --quiet --ff-only "$merge"
echo "sync-merge.sh: merged upstream ${upstream:0:8} as ${merge:0:8}"
