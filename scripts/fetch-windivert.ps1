<#
.SYNOPSIS
  Download the official WinDivert 2.2.2-A release, verify its SHA-256 against
  third_party\windivert.sha256 and extract it to .cache\windivert\.

.NOTES
  The driver and DLL are used exactly as published (they are signed upstream).
  Works on Windows PowerShell 5.1 and PowerShell 7.
#>
[CmdletBinding()]
param(
    [string]$Destination = (Join-Path $PSScriptRoot '..\.cache\windivert')
)

$ErrorActionPreference = 'Stop'
$version = '2.2.2-A'
$zipName = "WinDivert-$version.zip"
$url = "https://github.com/basil00/WinDivert/releases/download/v2.2.2/$zipName"

$hashFile = Join-Path $PSScriptRoot '..\third_party\windivert.sha256'
$line = Get-Content -LiteralPath $hashFile | Where-Object { $_ -match "^[0-9A-Fa-f]{64}\s+$([regex]::Escape($zipName))$" } | Select-Object -First 1
if (-not $line) { throw "No SHA-256 entry for $zipName in $hashFile" }
$expected = $line.Substring(0, 64).ToUpperInvariant()

$Destination = [IO.Path]::GetFullPath($Destination)
$extracted = Join-Path $Destination "WinDivert-$version"
$zipPath = Join-Path $Destination $zipName
New-Item -ItemType Directory -Force -Path $Destination | Out-Null

if (-not (Test-Path -LiteralPath $zipPath)) {
    Write-Host "Downloading $url"
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $partial = "$zipPath.partial"
    Invoke-WebRequest -Uri $url -OutFile $partial -UseBasicParsing
    Move-Item -LiteralPath $partial -Destination $zipPath -Force
}

$actual = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToUpperInvariant()
if ($actual -ne $expected) {
    Remove-Item -LiteralPath $zipPath -Force
    throw "SHA-256 mismatch for $zipName. Expected $expected, got $actual. The download was deleted."
}
Write-Host "SHA-256 verified: $actual"

if (Test-Path -LiteralPath $extracted) { Remove-Item -LiteralPath $extracted -Recurse -Force }
Expand-Archive -LiteralPath $zipPath -DestinationPath $Destination -Force

$x64 = Join-Path $extracted 'x64'
foreach ($file in 'WinDivert.dll', 'WinDivert.lib', 'WinDivert64.sys') {
    if (-not (Test-Path -LiteralPath (Join-Path $x64 $file))) { throw "Missing $file in $x64" }
}
Write-Host "WinDivert $version ready: $x64"
