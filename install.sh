#!/bin/sh
# Installs a version of Kent the Clerk, and its launcher, for the current user (S-0111).
#
#   sh install.sh                    installs the latest release
#   sh install.sh --version 0.9.0    installs that release
#
# It downloads the release's zip for this platform and the release's SHA256SUMS, checks the zip against its
# checksum, and places the version in <root>/versions/<version>/ beside any versions already installed. It
# replaces the launcher, <root>/kb, only if the one in the zip is newer, and adds <root> to your PATH the
# first time, in ~/.profile, and in ~/.bashrc and ~/.zshenv where those shells are used. Nothing is run from
# the download except, after its checksum is checked, the launcher's report of its own version.
#
# Other options:
#   --root <folder>   where Kent is installed (default ${XDG_DATA_HOME:-$HOME/.local/share}/kenttheclerk)
#   --zip <zip>       install from a zip already downloaded; SHA256SUMS must be beside it
#   --force           reinstall a version that is already installed
#   --no-path         do not change your PATH
set -eu

# The GitHub repository releases are published from.
REPO="OWNER/KentTheClerk"

version=latest
root="${XDG_DATA_HOME:-$HOME/.local/share}/kenttheclerk"
zip=""
force=false
modify_path=true

fail() { echo "install: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--version) [ $# -ge 2 ] || fail "--version needs a value"; version="$2"; shift 2 ;;
	--root) [ $# -ge 2 ] || fail "--root needs a value"; root="$2"; shift 2 ;;
	--zip) [ $# -ge 2 ] || fail "--zip needs a value"; zip="$2"; shift 2 ;;
	--force) force=true; shift ;;
	--no-path) modify_path=false; shift ;;
	*) fail "unknown option $1" ;;
	esac
done

# compare_versions A B prints -1, 0 or 1, ordering MAJOR.MINOR.PATCH[-pre] as semantic versioning does.
compare_versions() {
	awk -v a="$1" -v b="$2" 'BEGIN {
		ia = index(a, "-"); ib = index(b, "-")
		ac = ia ? substr(a, 1, ia - 1) : a; ap = ia ? substr(a, ia + 1) : ""
		bc = ib ? substr(b, 1, ib - 1) : b; bp = ib ? substr(b, ib + 1) : ""
		split(ac, an, "."); split(bc, bn, ".")
		for (i = 1; i <= 3; i++) if (an[i] + 0 != bn[i] + 0) { print (an[i] + 0 < bn[i] + 0) ? -1 : 1; exit }
		if (ap == bp) print 0; else if (ap == "") print 1; else if (bp == "") print -1; else print (ap < bp) ? -1 : 1
	}'
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
	else fail "neither sha256sum nor shasum is available to check the download"; fi
}

download() {
	if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then wget -q "$1" -O "$2"
	else fail "neither curl nor wget is available to download Kent"; fi
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
MINGW* | MSYS* | CYGWIN*) os=windows ;;
*) fail "no release is built for this system ($(uname -s))" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "no release is built for this processor ($(uname -m))" ;;
esac
exe=kb
[ "$os" = windows ] && exe=kb.exe
command -v unzip >/dev/null 2>&1 || fail "unzip is needed to unpack the download"

work=$(mktemp -d "${TMPDIR:-/tmp}/kent-install.XXXXXX")
trap 'rm -rf "$work"' EXIT

# the zip and its checksums
if [ -n "$zip" ]; then
	[ -f "$zip" ] || fail "no such file: $zip"
	sums="$(dirname "$zip")/SHA256SUMS"
	[ -f "$sums" ] || fail "no SHA256SUMS beside $zip"
	base=$(basename "$zip")
	version=$(echo "$base" | sed -n "s/^kent-\(.*\)-$os-$arch\.zip\$/\1/p")
	[ -n "$version" ] || fail "$zip is not named kent-<version>-$os-$arch.zip"
else
	case "$REPO" in OWNER/*) fail "this script does not name the repository it installs from; set REPO" ;; esac
	if [ "$version" = latest ]; then
		download "https://api.github.com/repos/$REPO/releases/latest" "$work/latest.json"
		version=$(sed -n 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/p' "$work/latest.json" | head -n 1)
		[ -n "$version" ] || fail "cannot tell which release is the latest"
	fi
	version=${version#v}
	url="https://github.com/$REPO/releases/download/v$version"
	zip="$work/kent-$version-$os-$arch.zip"
	sums="$work/SHA256SUMS"
	echo "downloading Kent $version for $os-$arch"
	download "$url/kent-$version-$os-$arch.zip" "$zip" || fail "cannot download Kent $version from $url"
	download "$url/SHA256SUMS" "$sums" || fail "cannot download SHA256SUMS from $url"
fi
echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail "$version is not a version"
name="kent-$version-$os-$arch"

# the checksum
want=$(awk -v f="$name.zip" '{ n = $2; sub(/^\*/, "", n) } n == f { print tolower($1); exit }' "$sums")
[ -n "$want" ] || fail "SHA256SUMS has no checksum for $name.zip"
got=$(sha256 "$zip")
[ "$got" = "$want" ] || fail "$name.zip does not match its checksum (expected $want, got $got); nothing was installed"

# unpack and check the layout
unzip -q "$zip" -d "$work"
src="$work/$name"
[ -f "$src/versions/$version/$exe" ] && [ -f "$src/$exe" ] || fail "$name.zip does not hold $exe and versions/$version/$exe"
chmod +x "$src/$exe" "$src/versions/$version/$exe"

# the version, beside the others
dest="$root/versions/$version"
mkdir -p "$root/versions"
if [ -d "$dest" ] && ! $force; then
	echo "Kent $version is already installed in $dest (pass --force to reinstall it)"
else
	staging="$root/versions/.$version.installing"
	rm -rf "$staging"
	cp -R "$src/versions/$version" "$staging"
	rm -rf "$dest"
	mv "$staging" "$dest"
	echo "installed Kent $version in $dest"
fi

# the launcher, only if the one in the zip is newer
launcher="$root/$exe"
bundled=$("$src/$exe" --launcher version)
replace=true
if [ -f "$launcher" ]; then
	current=$("$launcher" --launcher version 2>/dev/null || echo 0.0.0)
	echo "$current" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+' || current=0.0.0
	if [ "$(compare_versions "$bundled" "$current")" -le 0 ]; then
		replace=false
		echo "kept the launcher $current (the zip's is $bundled)"
	fi
fi
if $replace; then
	# replacing by rename leaves a running launcher undisturbed
	cp "$src/$exe" "$launcher.new"
	mv -f "$launcher.new" "$launcher"
	echo "installed the launcher $bundled in $launcher"
fi

# the PATH, the first time
case ":$PATH:" in
*":$root:"*) on_path=true ;;
*) on_path=false ;;
esac
if ! $on_path; then
	if ! $modify_path; then
		echo "$root is not on your PATH; add it to run kb from anywhere"
	else
		line="export PATH=\"$root:\$PATH\"  # Kent the Clerk"
		files="$HOME/.profile"
		[ -f "$HOME/.bashrc" ] && files="$files $HOME/.bashrc"
		case "${SHELL:-}" in */zsh) files="$files $HOME/.zshenv" ;; esac
		[ -f "$HOME/.zshenv" ] && case "$files" in *zshenv*) ;; *) files="$files $HOME/.zshenv" ;; esac
		for f in $files; do
			grep -qsF "$line" "$f" || printf '\n%s\n' "$line" >>"$f"
		done
		echo "added $root to your PATH in $files; open a new terminal for it to take effect"
	fi
fi

echo
"$launcher" --launcher || true
