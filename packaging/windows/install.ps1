<#
.SYNOPSIS
  Installs the Orion Belt clients (osh, ocp, oadmin) on Windows.

.DESCRIPTION
  No administrator rights are needed: the clients go to
  %LOCALAPPDATA%\Programs\orion-belt, which is added to your PATH. Every
  download is checked against the release's checksums.txt.

    irm https://pkgs.orion-belt.dev/install.ps1 | iex

  With options, download it first:
    .\install.ps1 -Version v1.4.0
    .\install.ps1 -Uninstall

.PARAMETER Version
  Release tag to install (default: the latest).
.PARAMETER InstallDir
  Where the clients go (default: %LOCALAPPDATA%\Programs\orion-belt).
.PARAMETER Uninstall
  Remove the clients and their PATH entry. Your client.yaml is kept.
#>
[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\orion-belt"),
    [switch]$Uninstall
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue" # Invoke-WebRequest is far faster without it
$Repo = "orion-belt-dev/orion-belt"
$Pkgs = if ($env:ORION_PKGS_URL) { $env:ORION_PKGS_URL.TrimEnd("/") } else { "https://pkgs.orion-belt.dev" }
$Tools = @("osh.exe", "ocp.exe", "oadmin.exe")

function Say([string]$msg) { Write-Host "orion-belt-install: $msg" }

function Get-Arch {
    $a = $env:PROCESSOR_ARCHITEW6432
    if (-not $a) { $a = $env:PROCESSOR_ARCHITECTURE }
    switch ($a) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        "x86" { return "386" }
        default { throw "unsupported CPU architecture $a (release builds: x64, ARM64, x86)" }
    }
}

function Resolve-Version {
    if ($Version) {
        if ($Version.StartsWith("v")) { return $Version } else { return "v$Version" }
    }
    $tag = (Invoke-WebRequest -UseBasicParsing -Uri "$Pkgs/TAG").Content.Trim()
    if (-not $tag.StartsWith("v")) { throw "could not find the latest release (use -Version)" }
    return $tag
}

function Get-BaseUrl([string]$tag) {
    if ($env:ORION_RELEASE_URL) { return $env:ORION_RELEASE_URL.TrimEnd("/") }
    return "https://github.com/$Repo/releases/download/$tag"
}

function Get-Verified([string]$base, [string]$name, [string]$dest) {
    $sums = Join-Path $env:TEMP "orion-belt-checksums.txt"
    Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $sums
    Say "downloading $name"
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile $dest
    $line = Get-Content $sums | Where-Object { ($_ -split "\s+")[1] -eq $name } | Select-Object -First 1
    if (-not $line) { throw "$name is not listed in checksums.txt" }
    $want = ($line -split "\s+")[0].ToLowerInvariant()
    $got = (Get-FileHash -Algorithm SHA256 -Path $dest).Hash.ToLowerInvariant()
    if ($got -ne $want) { Remove-Item $dest -Force; throw "checksum mismatch for $name" }
}

function Set-UserPath([string]$dir, [bool]$add) {
    $path = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($path) { $parts = $path -split ";" | Where-Object { $_ -and ($_.TrimEnd("\") -ne $dir.TrimEnd("\")) } }
    if ($add) { $parts += $dir }
    [Environment]::SetEnvironmentVariable("Path", ($parts -join ";"), "User")
}

if ($Uninstall) {
    foreach ($t in $Tools) { Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $InstallDir $t) }
    Set-UserPath $InstallDir $false
    Say "removed the clients from $InstallDir"
    return
}

$arch = Get-Arch
$tag = Resolve-Version
$ver = $tag.TrimStart("v")
$zip = "orion-belt_tools_${ver}_windows_${arch}.zip"
$tmp = Join-Path $env:TEMP ("orion-belt-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Get-Verified (Get-BaseUrl $tag) $zip (Join-Path $tmp $zip)
    Expand-Archive -Path (Join-Path $tmp $zip) -DestinationPath (Join-Path $tmp "x")
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    foreach ($t in $Tools) {
        Copy-Item -Force (Join-Path $tmp "x\$t") (Join-Path $InstallDir $t)
    }
    Set-UserPath $InstallDir $true
    if (-not (($env:Path -split ";") -contains $InstallDir)) { $env:Path = "$env:Path;$InstallDir" }
    Say "installed osh, ocp and oadmin $tag in $InstallDir (on your PATH; open a new terminal)"
} finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
}
