#!/bin/sh
# Builds a development build of kb (S-0110): its version is the one in identity.go with "-dev" added, and
# its acts are stamped with the commit it was built from and whether the tree was dirty.
#
#   build-dev.sh                    builds kb in this folder
#   build-dev.sh --install [root]   also builds the launcher and installs both where the launcher finds
#                                   them: <root>/kb and <root>/versions/<version>/kb
#
# root defaults to ${XDG_DATA_HOME:-$HOME/.local/share}/kenttheclerk. Put it on your PATH to run kb anywhere.
set -e
cd "$(dirname "$0")"
install=false
root="${XDG_DATA_HOME:-$HOME/.local/share}/kenttheclerk"
if [ "$1" = "--install" ]; then
	install=true
	[ -n "$2" ] && root="$2"
fi
commit=$(git rev-parse HEAD)
dirty=false
[ -n "$(git status --porcelain)" ] && dirty=true
exe=kb
case "$(go env GOOS)" in windows) exe=kb.exe ;; esac
go build -ldflags "-X kenttheclerk.dev=true -X kenttheclerk.commit=$commit -X kenttheclerk.dirty=$dirty" -o "$exe" ./cmd/kb
info=$(./"$exe" version)
echo "$info"
if $install; then
	version=$(echo "$info" | sed -n 's/^system_version: //p')
	mkdir -p "$root/versions/$version"
	cp "$exe" "$root/versions/$version/$exe"
	go build -o "$root/$exe" ./cmd/launcher
	echo "installed $version in $root/versions/$version, and the launcher in $root"
fi
