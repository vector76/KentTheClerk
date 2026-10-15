# Installs the version of Kent the Clerk this script is attached to, and its launcher, for the current user
# (S-0116). Each release carries its own copy of this script, with that release's version and the checksums
# of its zips written in below when the release is built. To install another version, use the script
# attached to that version's release.
#
#   powershell -ExecutionPolicy Bypass -File .\install.ps1
#
# It downloads this release's zip for this platform, checks it against the checksum written in below, and
# places the version in <Root>\versions\<version>\ beside any versions already installed. It replaces the
# launcher, <Root>\kb.exe, only if the one in the zip is newer, and adds <Root> to your user PATH the first
# time. Nothing is run from the download except, after its checksum is checked, the launcher's report of
# its own version.
#
# Options:
#   -Root <folder>     where Kent is installed (default %LOCALAPPDATA%\Programs\KentTheClerk)
#   -ZipFile <zip>     install from this release's zip, already downloaded
#   -Force             reinstall the version if it is already installed
#   -NoPath            do not change your PATH
#
# In the source repository this file is a template, with no version or checksums. There it installs only
# from -ZipFile, taking the version from the zip's name and the checksum from a SHA256SUMS beside it, for
# testing a build before it is released.
param(
    [string]$Root = (Join-Path $env:LOCALAPPDATA 'Programs\KentTheClerk'),
    [string]$ZipFile,
    [switch]$Force,
    [switch]$NoPath
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

# The GitHub repository releases are published from.
$Repo = 'vector76/KentTheClerk'

# The release this script installs, written in when the release is built.
$Version = '@VERSION@'
$Checksums = @{
    # @CHECKSUMS@
}
$Template = $Version -eq ('@' + 'VERSION@')

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
    # the zip
    if ($ZipFile) {
        $zip = (Resolve-Path $ZipFile).Path
        if ((Split-Path $zip -Leaf) -notmatch '^kent-(.+)-windows-(amd64|arm64)\.zip$') { Fail "$zip is not named kent-<version>-windows-<arch>.zip" }
        $zipVersion = $Matches[1]
        if ($Template) {
            # testing a build: the checksums come from beside the zip
            $Version = $zipVersion
            $sums = Join-Path (Split-Path $zip) 'SHA256SUMS'
            if (-not (Test-Path $sums)) { Fail "no SHA256SUMS beside $zip" }
            foreach ($l in Get-Content $sums) {
                if ($l -match '^([0-9a-fA-F]{64})\s+\*?(\S+)$') { $Checksums[$Matches[2]] = $Matches[1].ToLower() }
            }
        } elseif ($zipVersion -ne $Version) {
            Fail "$zip is Kent $zipVersion, and this script installs Kent $Version; use the script attached to the release of $zipVersion"
        }
    } else {
        if ($Template) { Fail "this is the template from Kent's source repository, which names no release; use the install script attached to a release, or -ZipFile" }
        $zip = Join-Path $work "kent-$Version-windows-$arch.zip"
        $url = "https://github.com/$Repo/releases/download/v$Version/kent-$Version-windows-$arch.zip"
        Write-Host "downloading Kent $Version for windows-$arch"
        try { Invoke-WebRequest $url -OutFile $zip } catch { Fail "cannot download $url ($($_.Exception.Message))" }
    }
    if ($Version -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') { Fail "$Version is not a version" }
    $name = "kent-$Version-windows-$arch"

    # the checksum
    $want = $Checksums["$name.zip"]
    if (-not $want) { Fail "this script has no checksum for $name.zip" }
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

    # what to do next (S-0126)
    Write-Host ""
    Write-Host "Next: open Claude Code in your project (restart it if it is already open, so it finds kb on your"
    Write-Host "PATH) and tell your agent:"
    Write-Host ""
    Write-Host "    Run kb setup and follow its instructions."
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
