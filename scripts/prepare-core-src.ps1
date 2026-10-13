<#
.SYNOPSIS
  Prepare the ProxyBridge core sources for building: copy the pinned submodule
  sources to .cache\core-src, normalize line endings to LF and apply every
  third_party\patches\*.patch in name order.

.NOTES
  Each patch is verified after applying (a reverse check must succeed), so a
  patch can never be skipped silently. Needs git; no compiler required.
  Works on Windows PowerShell 5.1 and PowerShell 7.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$upstreamSrc = Join-Path $root 'third_party\ProxyBridge\Windows\src'
$patchDir = Join-Path $root 'third_party\patches'
$relWork = '.cache/core-src'
$workDir = Join-Path $root '.cache\core-src'

if (-not (Test-Path -LiteralPath (Join-Path $upstreamSrc 'ProxyBridge.c'))) {
    throw "ProxyBridge sources not found. Run: git submodule update --init"
}

if (Test-Path -LiteralPath $workDir) { Remove-Item -LiteralPath $workDir -Recurse -Force }
New-Item -ItemType Directory -Force -Path $workDir | Out-Null
Copy-Item -Path (Join-Path $upstreamSrc '*') -Destination $workDir -Recurse

# Byte-level CRLF -> LF so patches apply the same way whatever core.autocrlf is.
Get-ChildItem -LiteralPath $workDir -Recurse -File | Where-Object { $_.Extension -in '.c', '.h' } | ForEach-Object {
    $bytes = [IO.File]::ReadAllBytes($_.FullName)
    $out = New-Object System.Collections.Generic.List[byte] ($bytes.Length)
    for ($i = 0; $i -lt $bytes.Length; $i++) {
        if ($bytes[$i] -eq 13 -and $i + 1 -lt $bytes.Length -and $bytes[$i + 1] -eq 10) { continue }
        $out.Add($bytes[$i])
    }
    [IO.File]::WriteAllBytes($_.FullName, $out.ToArray())
}

$patches = @(Get-ChildItem -LiteralPath $patchDir -Filter '*.patch' -ErrorAction SilentlyContinue | Sort-Object Name)
foreach ($patch in $patches) {
    # Run from the repository root with --directory: inside the repository,
    # git apply resolves paths from the top level and would otherwise skip
    # files under .cache without complaint.
    & git -C $root apply --directory=$relWork --whitespace=nowarn $patch.FullName
    if ($LASTEXITCODE -ne 0) { throw "Patch failed: $($patch.Name)" }
    & git -C $root apply --directory=$relWork --reverse --check $patch.FullName
    if ($LASTEXITCODE -ne 0) { throw "Patch did not take effect: $($patch.Name)" }
    Write-Host "Applied $($patch.Name)"
}
Write-Host "Core sources ready: $workDir ($($patches.Count) patch(es))"
