# Builds a development build of kb (S-0110): its version is the one in identity.go with "-dev" added, and
# its acts are stamped with the commit it was built from and whether the tree was dirty.
#
#   build-dev.ps1             builds kb.exe in this folder
#   build-dev.ps1 -Install    also builds the launcher and installs both where the launcher finds them:
#                             <Root>\kb.exe and <Root>\versions\<version>\kb.exe
#
# -Root defaults to %LOCALAPPDATA%\Programs\KentTheClerk. Put that folder on your PATH to run kb anywhere.
param(
    [switch]$Install,
    [string]$Root = (Join-Path $env:LOCALAPPDATA 'Programs\KentTheClerk')
)
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    $commit = (git rev-parse HEAD).Trim()
    $dirty = if (git status --porcelain) { 'true' } else { 'false' }
    go build -ldflags "-X kenttheclerk.dev=true -X kenttheclerk.commit=$commit -X kenttheclerk.dirty=$dirty" -o kb.exe ./cmd/kb
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    $info = ./kb.exe version
    $info
    if ($Install) {
        $version = (($info | Select-String '^system_version: (.+)$').Matches[0].Groups[1].Value).Trim()
        $dest = Join-Path $Root "versions\$version"
        New-Item -ItemType Directory -Force $dest | Out-Null
        Copy-Item kb.exe (Join-Path $dest 'kb.exe') -Force
        go build -o (Join-Path $Root 'kb.exe') ./cmd/launcher
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        "installed $version in $dest, and the launcher in $Root"
    }
} finally {
    Pop-Location
}
