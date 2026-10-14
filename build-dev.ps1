# Builds a development build of kb (S-0110) in this folder: its version is the one in identity.go with
# "-dev" added, and its acts are stamped with the commit it was built from and whether the tree was dirty.
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    $commit = (git rev-parse HEAD).Trim()
    $dirty = if (git status --porcelain) { 'true' } else { 'false' }
    go build -ldflags "-X kenttheclerk.dev=true -X kenttheclerk.commit=$commit -X kenttheclerk.dirty=$dirty" -o kb.exe ./cmd/kb
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    ./kb.exe version
} finally {
    Pop-Location
}
