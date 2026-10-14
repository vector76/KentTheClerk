# Installs a version of Kent the Clerk, and its launcher, for the current user (S-0111).
#
#   ./install.ps1                    installs the latest release
#   ./install.ps1 -Version 0.9.0     installs that release
#
# It downloads the release's zip for this platform and the release's SHA256SUMS, checks the zip against its
# checksum, and places the version in <Root>\versions\<version>\ beside any versions already installed. It
# replaces the launcher, <Root>\kb.exe, only if the one in the zip is newer, and adds <Root> to your user
# PATH the first time. Nothing is run from the download except, after its checksum is checked, the
# launcher's report of its own version.
#
# Other options:
#   -Root <folder>     where Kent is installed (default %LOCALAPPDATA%\Programs\KentTheClerk)
#   -ZipFile <zip>     install from a zip already downloaded; SHA256SUMS must be beside it
#   -Force             reinstall a version that is already installed
#   -NoPath            do not change your PATH
param(
    [string]$Version = 'latest',
    [string]$Root = (Join-Path $env:LOCALAPPDATA 'Programs\KentTheClerk'),
    [string]$ZipFile,
    [switch]$Force,
    [switch]$NoPath
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

# The GitHub repository releases are published from.
$Repo = 'OWNER/KentTheClerk'

function Fail([string]$msg) { Write-Host "install: $msg" -ForegroundColor Red; exit 1 }

# Compares MAJOR.MINOR.PATCH[-pre] versions as semantic versioning does; returns -1, 0 or 1.
function Compare-Version([string]$a, [string]$b) {
    $ac, $ap = $a.Split('-', 2); $bc, $bp = $b.Split('-', 2)
    $an = $ac.Split('.') | ForEach-Object { [long]$_ }; $bn = $bc.Split('.') | ForEach-Object { [long]$_ }
    for ($i = 0; $i -lt 3; $i++) { if ($an[$i] -ne $bn[$i]) { return [Math]::Sign($an[$i] - $bn[$i]) } }
    if ($ap -eq $bp) { return 0 }
    if (-not $ap) { return 1 }
    if (-not $bp) { return -1 }
    return [Math]::Sign([string]::CompareOrdinal($ap, $bp))
}

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Fail "no release is built for this processor ($env:PROCESSOR_ARCHITECTURE)" }
}

$work = Join-Path ([IO.Path]::GetTempPath()) ("kent-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $work | Out-Null
try {
    # the zip and its checksums
    if ($ZipFile) {
        $zip = (Resolve-Path $ZipFile).Path
        $sums = Join-Path (Split-Path $zip) 'SHA256SUMS'
        if (-not (Test-Path $sums)) { Fail "no SHA256SUMS beside $zip" }
        if ((Split-Path $zip -Leaf) -notmatch '^kent-(.+)-windows-(amd64|arm64)\.zip$') { Fail "$zip is not named kent-<version>-windows-<arch>.zip" }
        $Version = $Matches[1]
    } else {
        if ($Repo -like 'OWNER/*') { Fail "this script does not name the repository it installs from; set `$Repo" }
        if ($Version -eq 'latest') {
            $tag = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest").tag_name
            $Version = $tag.TrimStart('v')
        }
        $Version = $Version.TrimStart('v')
        $base = "https://github.com/$Repo/releases/download/v$Version"
        $zip = Join-Path $work "kent-$Version-windows-$arch.zip"
        $sums = Join-Path $work 'SHA256SUMS'
        Write-Host "downloading Kent $Version for windows-$arch"
        try {
            Invoke-WebRequest "$base/kent-$Version-windows-$arch.zip" -OutFile $zip
            Invoke-WebRequest "$base/SHA256SUMS" -OutFile $sums
        } catch { Fail "cannot download Kent $Version from $base ($($_.Exception.Message))" }
    }
    if ($Version -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') { Fail "$Version is not a version" }
    $name = "kent-$Version-windows-$arch"

    # the checksum
    $line = Get-Content $sums | Where-Object { $_ -match "^([0-9a-fA-F]{64})\s+\*?$([regex]::Escape("$name.zip"))$" } | Select-Object -First 1
    if (-not $line) { Fail "SHA256SUMS has no checksum for $name.zip" }
    $want = $Matches[1].ToLower()
    $got = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want) { Fail "$name.zip does not match its checksum (expected $want, got $got); nothing was installed" }

    # unpack and check the layout
    Expand-Archive $zip -DestinationPath $work
    $src = Join-Path $work $name
    $srcVersion = Join-Path $src "versions\$Version"
    $srcLauncher = Join-Path $src 'kb.exe'
    if (-not (Test-Path (Join-Path $srcVersion 'kb.exe')) -or -not (Test-Path $srcLauncher)) { Fail "$name.zip does not hold kb.exe and versions\$Version\kb.exe" }

    # the version, beside the others
    $versions = Join-Path $Root 'versions'
    $dest = Join-Path $versions $Version
    New-Item -ItemType Directory -Force $versions | Out-Null
    if ((Test-Path $dest) -and -not $Force) {
        Write-Host "Kent $Version is already installed in $dest (pass -Force to reinstall it)"
    } else {
        $staging = Join-Path $versions ".$Version.installing"
        if (Test-Path $staging) { Remove-Item -Recurse -Force $staging }
        Copy-Item -Recurse $srcVersion $staging
        if (Test-Path $dest) {
            try { Remove-Item -Recurse -Force $dest } catch { Remove-Item -Recurse -Force $staging; Fail "cannot replace $dest; is a kb of that version running?" }
        }
        Rename-Item $staging $Version
        Write-Host "installed Kent $Version in $dest"
    }

    # the launcher, only if the one in the zip is newer; a launcher set aside earlier while it was running
    # is deleted once it no longer is
    $launcher = Join-Path $Root 'kb.exe'
    Remove-Item -Force "$launcher.old" -ErrorAction SilentlyContinue
    $bundled = (& $srcLauncher --launcher version).Trim()
    $firstInstall = -not (Test-Path $launcher)
    $replace = $firstInstall
    if (-not $firstInstall) {
        $current = try { (& $launcher --launcher version).Trim() } catch { '0.0.0' }
        if ($current -notmatch '^\d+\.\d+\.\d+') { $current = '0.0.0' }
        $replace = (Compare-Version $bundled $current) -gt 0
        if (-not $replace) { Write-Host "kept the launcher $current (the zip's is $bundled)" }
    }
    if ($replace) {
        # A running executable cannot be overwritten on Windows, but it can be renamed out of the way.
        $old = "$launcher.old"
        if (Test-Path $old) { Remove-Item -Force $old -ErrorAction SilentlyContinue }
        if (Test-Path $launcher) { Rename-Item $launcher (Split-Path $old -Leaf) }
        Copy-Item $srcLauncher $launcher
        Write-Host "installed the launcher $bundled in $launcher"
    }

    # the PATH, the first time
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $onPath = ($userPath -split ';' | Where-Object { $_.TrimEnd('\') -ieq $Root.TrimEnd('\') }).Count -gt 0
    if (-not $onPath) {
        if ($NoPath) {
            Write-Host "$Root is not on your PATH; add it to run kb from anywhere"
        } else {
            $newPath = if ($userPath) { "$userPath;$Root" } else { $Root }
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
            $env:Path = "$env:Path;$Root"
            Write-Host "added $Root to your user PATH; open a new terminal for it to take effect there"
        }
    }

    Write-Host ""
    & $launcher --launcher
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
