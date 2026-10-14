#!/bin/sh
# Builds a development build of kb (S-0110) in this folder: its version is the one in identity.go with
# "-dev" added, and its acts are stamped with the commit it was built from and whether the tree was dirty.
set -e
cd "$(dirname "$0")"
commit=$(git rev-parse HEAD)
dirty=false
[ -n "$(git status --porcelain)" ] && dirty=true
exe=kb
case "$(go env GOOS)" in windows) exe=kb.exe ;; esac
go build -ldflags "-X kenttheclerk.dev=true -X kenttheclerk.commit=$commit -X kenttheclerk.dirty=$dirty" -o "$exe" ./cmd/kb
./"$exe" version
