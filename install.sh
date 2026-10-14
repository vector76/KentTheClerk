#!/bin/sh
# Installs the version of Kent the Clerk this script is attached to, and its launcher, for the current user
# (S-0116). Each release carries its own copy of this script, with that release's version and the checksums
# of its zips written in below when the release is built. To install another version, use the script
# attached to that version's release.
#
#   sh install.sh
#
# It downloads this release's zip for this platform, checks it against the checksum written in below, and
# places the version in <root>/versions/<version>/ beside any versions already installed. It replaces the
# launcher, <root>/kb, only if the one in the zip is newer, and adds <root> to your PATH the first time, in
# ~/.profile, and in ~/.bashrc and ~/.zshenv where those shells are used. Nothing is run from the download
# except, after its checksum is checked, the launcher's report of its own version.
#
# Options:
#   --root <folder>   where Kent is installed (default ${XDG_DATA_HOME:-$HOME/.local/share}/kenttheclerk)
#   --zip <zip>       install from this release's zip, already downloaded
#   --force           reinstall the version if it is already installed
#   --no-path         do not change your PATH
#
# In the source repository this file is a template, with no version or checksums. There it installs only
# from --zip, taking the version from the zip's name and the checksums from a SHA256SUMS beside it, for
# testing a build before it is released.
set -eu

# The GitHub repository releases are published from.
REPO="vector76/KentTheClerk"

# The release this script installs, written in when the release is built: "<sha256>  <zip>" per line.
VERSION="@VERSION@"
CHECKSUMS="
@CHECKSUMS@
"
template=false
[ "$VERSION" = "@""VERSION@" ] && template=true

root="${XDG_DATA_HOME:-$HOME/.local/share}/kenttheclerk"
zip=""
force=false
modify_path=true

fail() { echo "install: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
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

# the zip
version="$VERSION"
sums="$work/checksums"
printf '%s\n' "$CHECKSUMS" >"$sums"
if [ -n "$zip" ]; then
	[ -f "$zip" ] || fail "no such file: $zip"
	zip_version=$(basename "$zip" | sed -n "s/^kent-\(.*\)-$os-$arch\.zip\$/\1/p")
	[ -n "$zip_version" ] || fail "$zip is not named kent-<version>-$os-$arch.zip"
	if $template; then
		# testing a build: the checksums come from beside the zip
		version="$zip_version"
		[ -f "$(dirname "$zip")/SHA256SUMS" ] || fail "no SHA256SUMS beside $zip"
		cp "$(dirname "$zip")/SHA256SUMS" "$sums"
	elif [ "$zip_version" != "$version" ]; then
		fail "$zip is Kent $zip_version, and this script installs Kent $version; use the script attached to the release of $zip_version"
	fi
else
	$template && fail "this is the template from Kent's source repository, which names no release; use the install script attached to a release, or --zip"
	url="https://github.com/$REPO/releases/download/v$version/kent-$version-$os-$arch.zip"
	zip="$work/kent-$version-$os-$arch.zip"
	echo "downloading Kent $version for $os-$arch"
	download "$url" "$zip" || fail "cannot download $url"
fi
echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail "$version is not a version"
name="kent-$version-$os-$arch"

# the checksum
want=$(awk -v f="$name.zip" '{ n = $2; sub(/^\*/, "", n) } n == f { print tolower($1); exit }' "$sums")
[ -n "$want" ] || fail "this script has no checksum for $name.zip"
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
