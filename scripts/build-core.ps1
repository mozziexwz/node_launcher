<#
.SYNOPSIS
  Build ProxyBridgeCore.dll (x64) from the pinned ProxyBridge submodule with MSVC.

.DESCRIPTION
  1. Copies third_party\ProxyBridge\Windows\src to .cache\core-src (the submodule stays pristine).
  2. Applies every third_party\patches\*.patch in name order with "git apply".
  3. Compiles with the same cl.exe flags as upstream compile.ps1 and links against WinDivert.
  4. Writes ProxyBridgeCore.dll, WinDivert.dll and WinDivert64.sys to build\core\.

  Requires Visual Studio 2022 (or Build Tools) with the C++ desktop workload,
  and WinDivert from scripts\fetch-windivert.ps1.
#>
[CmdletBinding()]
param(
    [string]$OutputDir = (Join-Path $PSScriptRoot '..\build\core')
)

$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$upstreamSrc = Join-Path $root 'third_party\ProxyBridge\Windows\src'
$patchDir = Join-Path $root 'third_party\patches'
$workDir = Join-Path $root '.cache\core-src'
$winDivert = Join-Path $root '.cache\windivert\WinDivert-2.2.2-A'
$OutputDir = [IO.Path]::GetFullPath($OutputDir)

if (-not (Test-Path -LiteralPath (Join-Path $upstreamSrc 'ProxyBridge.c'))) {
    throw "ProxyBridge sources not found. Run: git submodule update --init"
}
if (-not (Test-Path -LiteralPath (Join-Path $winDivert 'x64\WinDivert.lib'))) {
    throw "WinDivert not found. Run: .\scripts\fetch-windivert.ps1"
}

$vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
if (-not (Test-Path -LiteralPath $vswhere)) {
    throw ("MSVC not found. Install Visual Studio 2022 Build Tools with the C++ workload, e.g.`n" +
        "  winget install Microsoft.VisualStudio.2022.BuildTools --override `"--quiet --wait --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended`"")
}
$vsPath = & $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
if (-not $vsPath) { throw 'Visual Studio with the C++ x64 toolset was not found.' }
$vcvars = Join-Path $vsPath 'VC\Auxiliary\Build\vcvarsall.bat'

# Fresh copy of the pinned sources, then local patches.
if (Test-Path -LiteralPath $workDir) { Remove-Item -LiteralPath $workDir -Recurse -Force }
New-Item -ItemType Directory -Force -Path $workDir | Out-Null
Copy-Item -Path (Join-Path $upstreamSrc '*') -Destination $workDir -Recurse
$patches = @(Get-ChildItem -LiteralPath $patchDir -Filter '*.patch' -ErrorAction SilentlyContinue | Sort-Object Name)
foreach ($patch in $patches) {
    Write-Host "Applying $($patch.Name)"
    & git -C $workDir apply --whitespace=nowarn $patch.FullName
    if ($LASTEXITCODE -ne 0) { throw "Patch failed: $($patch.Name)" }
}

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null
$sources = @(Get-ChildItem -LiteralPath $workDir -Filter '*.c' | ForEach-Object { '"' + $_.FullName + '"' }) -join ' '
$dll = Join-Path $OutputDir 'ProxyBridgeCore.dll'

# Flags mirror upstream Windows\compile.ps1 (v4.1.0).
$clArgs = '/nologo /O2 /Ot /GL /Gy /W4 /wd4100 /wd4189 /wd4267 /wd4244 /wd4996 ' +
    '/D_CRT_SECURE_NO_WARNINGS /D_WINSOCK_DEPRECATED_NO_WARNINGS /DPROXYBRIDGE_EXPORTS /DNDEBUG ' +
    '/arch:SSE2 /fp:fast /GS /guard:cf /Qpar ' +
    "/I`"$winDivert\include`" $sources /LD " +
    "/Fo`"$workDir\\`" " +
    '/link /LTCG /OPT:REF /OPT:ICF /RELEASE /DYNAMICBASE /NXCOMPAT /guard:cf ' +
    "/LIBPATH:`"$winDivert\x64`" WinDivert.lib ws2_32.lib iphlpapi.lib " +
    "/OUT:`"$dll`""

$cmd = "`"$vcvars`" x64 >nul && cl.exe $clArgs"
cmd /c $cmd
if ($LASTEXITCODE -ne 0) { throw "cl.exe failed with exit code $LASTEXITCODE" }

Copy-Item -LiteralPath (Join-Path $winDivert 'x64\WinDivert.dll') -Destination $OutputDir -Force
Copy-Item -LiteralPath (Join-Path $winDivert 'x64\WinDivert64.sys') -Destination $OutputDir -Force
Get-ChildItem -LiteralPath $OutputDir | ForEach-Object {
    '{0}  {1}' -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash, $_.Name
}
