#!/usr/bin/env bash
# Turn a sing-box tree into Pasadizo.
#
# Run it at the root of a sing-box checkout (upstream or Pasadizo). It:
#   - changes the Go module path github.com/sagernet/sing-box to
#     github.com/pasadizo-net/pasadizo in go.mod, test/go.mod, every import and
#     every other build reference (ldflags -X, go run, build info lookups,
#     .proto go_package, Makefile, Dockerfile, release/ scripts);
#   - moves cmd/sing-box to cmd/pasadizo;
#   - names the program pasadizo in its help, usage and version output.
#
# The rename runs gofmt on the Go files it changed, because the new path sorts
# in another place among the imports of a block.
#
# Other sagernet modules (github.com/sagernet/sing, sing-tun, sing-quic, ...),
# URLs (https://github.com/SagerNet/sing-box/...), docs/, .github/, *.md and
# LICENSE are not changed. The rename gives the same result every time, so the
# weekly sync can apply it to upstream and merge.
#
# The script is idempotent: a second run changes nothing. If upstream changes a
# line that an edit below expects, the script stops with an error instead of
# leaving a half-renamed tree.
set -euo pipefail

old_module="github.com/sagernet/sing-box"
new_module="github.com/pasadizo-net/pasadizo"

fail() {
	echo "rename.sh: $*" >&2
	exit 1
}

[ -f go.mod ] || fail "run me at the root of the repository"
case "$(head -n 1 go.mod)" in
"module $old_module" | "module $new_module") ;;
*) fail "go.mod is neither sing-box nor pasadizo" ;;
esac

# Every tracked regular file that can carry a build reference gets the module
# path, where it stands alone: not after "/" or a letter (a URL or a GOPATH
# path), not after "Z" and a length byte (the go_package inside the raw
# protobuf descriptors of *.pb.go, whose length prefix must stay right), and
# not followed by "-", "." or a letter (github.com/sagernet/sing-box-*).
# "cmd/sing-box" is the directory of the main package. A file is written only
# when it changes, and the changed Go files are listed for gofmt below.
changed_go=()
# shellcheck disable=SC2016 # Perl code, not shell
while IFS= read -r -d '' file; do
	changed_go+=("$file")
done < <(git ls-files -z | perl -0 -ne '
	chomp;
	next if m{^(docs|\.github|clients)/} || m{\.md$} || $_ eq "LICENSE";
	next if -l $_ || !-f $_;
	my $file = $_;
	open(my $in, "<", $file) or die "$file: $!";
	my $text = do { local $/; <$in> };
	close($in);
	next if -B $file;
	my $orig = $text;
	$text =~ s{(?<![\w/.\-])(?<!Z.)\Qgithub.com/sagernet/sing-box\E(?![\w.\-])}{github.com/pasadizo-net/pasadizo}g;
	$text =~ s{(?<![\w\-])cmd/sing-box(?![\w.\-])}{cmd/pasadizo}g;
	next if $text eq $orig;
	open(my $out, ">", $file) or die "$file: $!";
	print $out $text;
	close($out) or die "$file: $!";
	print "$file\0" if $file =~ m{\.go$};
')

# gofmt sorts the imports of a block, and the new path sorts in another place
# ("pasadizo-net" before "sagernet/quic-go", where "sagernet/sing-box" came
# after it). Only the files that the rename changed and that gofmt then
# reports are written, so the result is the same on every run.
if [ "${#changed_go[@]}" -gt 0 ]; then
	unsorted=()
	while IFS= read -r file; do
		unsorted+=("$file")
	done < <(gofmt -l -- "${changed_go[@]}")
	if [ "${#unsorted[@]}" -gt 0 ]; then
		gofmt -w -- "${unsorted[@]}"
	fi
fi

# The main package.
if [ -d cmd/sing-box ]; then
	[ ! -e cmd/pasadizo ] || fail "both cmd/sing-box and cmd/pasadizo exist"
	mv cmd/sing-box cmd/pasadizo
fi
[ -d cmd/pasadizo ] || fail "cmd/pasadizo is missing"

# edit FILE PERL-SUBSTITUTION NEW-TEXT: apply the substitution, then require
# NEW-TEXT in the file, so a changed upstream line fails loudly.
edit() {
	local file="$1" expr="$2" expect="$3"
	[ -f "$file" ] || fail "$file is missing"
	perl -pi -e "$expr" "$file"
	grep -qF -- "$expect" "$file" || fail "$file: upstream changed the line for '$expect'; update rename.sh"
}

# The program name in the help and version output.
# shellcheck disable=SC2016 # Perl code, not shell
edit cmd/pasadizo/cmd.go 's{^(\s*Use:\s*)"sing-box",}{$1"pasadizo",}' '"pasadizo",'
edit cmd/pasadizo/cmd_version.go 's{"Print current version of sing-box"}{"Print current version of pasadizo"}' '"Print current version of pasadizo"'
edit cmd/pasadizo/cmd_version.go 's{"sing-box version " \+ C\.Version \+ "\\n\\n"}{"pasadizo version " + C.Version + "\\nbased on sing-box\\n\\n"}' '"pasadizo version " + C.Version + "\nbased on sing-box\n\n"'

# Hints that tell the user which command to run.
# shellcheck disable=SC2016 # Perl code, not shell
perl -pi -e '
	s{"sing-box (api|check|format|generate|geoip|geosite|merge|rule-set|run|tools|version)\b}{"pasadizo $1}g;
	s{\brun sing-box with\b}{run pasadizo with}g;
' cmd/pasadizo/*.go

# The import sections of the linter: Pasadizo's packages sort with sagernet's.
if [ -f .golangci.yml ]; then
	perl -pi -e 's{^(\s*- )prefix\(github\.com/sagernet/\)$}{$1prefix(github.com/sagernet/,github.com/pasadizo-net/)}' .golangci.yml
fi

# Nothing of the old module path may stay in a build reference (the raw
# protobuf descriptors keep it on purpose, see above).
if left=$(git grep -nIE '(^|[^[:alnum:]/.-])github\.com/sagernet/sing-box([^[:alnum:].-]|$)' -- . ':!docs' ':!.github' ':!clients' ':!*.md' ':!LICENSE' ':!cmd/sing-box' | grep -vE '\.pb\.go:[0-9]+:.*\\x'); then
	echo "$left" >&2
	fail "references to $old_module are left"
fi
