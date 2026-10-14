#!/bin/sh
# Writes a release's install scripts (S-0116): install.ps1 and install.sh from the top of the repository,
# with the release's version and the checksums of its zips written in.
#
#   fill-install-scripts.sh <version> <SHA256SUMS> <output folder>
#
# The release workflow runs it; it can be run locally to test a release's scripts before tagging.
set -eu
[ $# -eq 3 ] || { echo "usage: $0 <version> <SHA256SUMS> <output folder>" >&2; exit 2; }
version=$1
sums=$2
out=$3
repo=$(cd "$(dirname "$0")/.." && pwd)
echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || { echo "$version is not a version" >&2; exit 1; }
grep -Eq '^[0-9a-fA-F]{64}  ?\*?kent-' "$sums" || { echo "$sums holds no checksums of kent zips" >&2; exit 1; }
mkdir -p "$out"

# fill <template> <output> <format>: format is "ps1" or "sh", the form each checksum line takes
fill() {
	awk -v version="$version" -v format="$3" -v sums="$sums" '
		/@CHECKSUMS@/ {
			while ((getline line < sums) > 0) {
				split(line, f, /[ \t]+/)
				name = f[2]; sub(/^\*/, "", name)
				if (name !~ /^kent-.*\.zip$/) continue
				if (format == "ps1") printf "    \047%s\047 = \047%s\047\n", name, tolower(f[1])
				else printf "%s  %s\n", tolower(f[1]), name
			}
			close(sums)
			next
		}
		{ gsub(/@VERSION@/, version); sub(/\r$/, ""); print }
	' "$1" >"$2"
	if grep -q '@VERSION@\|@CHECKSUMS@' "$2"; then
		echo "$2 still holds a placeholder" >&2
		exit 1
	fi
}

fill "$repo/install.ps1" "$out/install.ps1" ps1
fill "$repo/install.sh" "$out/install.sh" sh
echo "wrote $out/install.ps1 and $out/install.sh for Kent $version"
