# install.ps1 — one-line installer / updater for the eidos binary on Windows.
#
# Canonical URL:
#     https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.ps1
#
# Usage:
#     iex (irm https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.ps1)
#
# Environment variables:
#     EIDOS_PREFIX   install prefix (default: $env:USERPROFILE\.local)
#     EIDOS_VERSION  pin a specific tag (default: latest, e.g. v0.8.0)
#
# Mirrors the contract of install.sh: fetch the matching release archive,
# verify SHA256 against checksums.txt (mandatory; aborts on mismatch),
# extract the .exe to <prefix>\bin, and add that directory to the user
# PATH if it isn't there already. Run `eidos.exe` from a fresh shell.

$ErrorActionPreference = 'Stop'

# Mute Invoke-WebRequest's progress UI — without this, a streaming download
# repaints the console hundreds of times per second over an SSH session and
# can dominate the runtime, especially under the legacy Windows PowerShell
# 5.1 host that ships with Windows 11.
$ProgressPreference = 'SilentlyContinue'

$repo    = 'LucianoXu/eidopsyche'
$binName = 'eidos.exe'
$project = 'eidos'

# ---------------------------------------------------------------- detect arch
function Get-EidosArch {
    switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
        ([System.Runtime.InteropServices.Architecture]::X64)   { 'amd64' }
        ([System.Runtime.InteropServices.Architecture]::Arm64) { 'arm64' }
        default {
            throw "install.ps1: unsupported architecture: $_"
        }
    }
}

# ------------------------------------------------------------- resolve version
function Resolve-EidosVersion {
    if ($env:EIDOS_VERSION) { return $env:EIDOS_VERSION }
    $api = "https://api.github.com/repos/$repo/releases/latest"
    try {
        return (Invoke-RestMethod -UseBasicParsing -Uri $api).tag_name
    } catch {
        throw "install.ps1: could not determine latest version from $api`: $_"
    }
}

# ------------------------------------------------------------- update PATH
function Add-ToUserPath([string]$dir) {
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($null -eq $current) { $current = '' }
    # Match against an exact path segment so we don't false-positive on
    # `..\.local\bin-old` when the new prefix is `.local\bin`.
    $segments = $current.Split(';', [StringSplitOptions]::RemoveEmptyEntries)
    if ($segments -contains $dir) { return $false }
    $updated = if ($current.Length -gt 0) { "$current;$dir" } else { $dir }
    [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
    return $true
}

# ------------------------------------------------------------- main
$arch    = Get-EidosArch
$version = Resolve-EidosVersion
$num     = $version.TrimStart('v')

$archive      = "${project}_${num}_windows_${arch}.zip"
$base         = "https://github.com/$repo/releases/download/$version"
$archiveUrl   = "$base/$archive"
$checksumsUrl = "$base/checksums.txt"

$prefix = if ($env:EIDOS_PREFIX) { $env:EIDOS_PREFIX } else { Join-Path $env:USERPROFILE '.local' }
$binDir = Join-Path $prefix 'bin'

Write-Host "install.ps1: target  windows/$arch"
Write-Host "install.ps1: version $version"
Write-Host "install.ps1: prefix  $prefix"
Write-Host "install.ps1: bindir  $binDir"
Write-Host "install.ps1: fetch   $archiveUrl"

# Stage to a temp directory we always clean up.
$stage = Join-Path ([System.IO.Path]::GetTempPath()) ("eidos-install-" + [guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Force -Path $stage | Out-Null
try {
    $zip       = Join-Path $stage $archive
    $checksums = Join-Path $stage 'checksums.txt'

    Invoke-WebRequest -UseBasicParsing -Uri $archiveUrl   -OutFile $zip
    Invoke-WebRequest -UseBasicParsing -Uri $checksumsUrl -OutFile $checksums

    # Verify SHA256 against the GoReleaser-format checksums file:
    #     <sha256>  <filename>
    $expected = $null
    foreach ($line in Get-Content -LiteralPath $checksums) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -eq 2 -and $parts[1].Trim() -eq $archive) {
            $expected = $parts[0].ToLowerInvariant()
            break
        }
    }
    if (-not $expected) {
        throw "install.ps1: $archive not listed in checksums.txt — refusing to install"
    }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
    if ($expected -ne $actual) {
        throw "install.ps1: SHA256 mismatch — refusing to install`n  expected: $expected`n  actual:   $actual"
    }
    Write-Host 'install.ps1: SHA256 ok'

    # Extract — Expand-Archive overwrites with -Force so a self-update is in-place.
    Expand-Archive -Force -LiteralPath $zip -DestinationPath $stage

    $src = Join-Path $stage $binName
    if (-not (Test-Path -LiteralPath $src)) {
        throw "install.ps1: $binName not found in archive"
    }

    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    $dst = Join-Path $binDir $binName
    Copy-Item -Force -LiteralPath $src -Destination $dst

    Write-Host ''
    Write-Host "✓ installed: $dst ($version)"

    if (Add-ToUserPath $binDir) {
        Write-Host "  note: $binDir added to user PATH — open a new shell for it to take effect"
    }
} finally {
    Remove-Item -Recurse -Force -LiteralPath $stage -ErrorAction SilentlyContinue
}
