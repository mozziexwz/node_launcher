# Hidden, local-only BAT wrapper smoke test. The only menu input is Q then Y.
param(
    [string]$LauncherPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'MSBOOST启动器_全程TCP测速.bat'),
    [int]$TimeoutMs = 15000
)

$ErrorActionPreference = 'Stop'
if ($TimeoutMs -lt 1000 -or $TimeoutMs -gt 30000) { throw 'Timeout must be between 1 and 30 seconds.' }
$launcher = (Resolve-Path -LiteralPath $LauncherPath).ProviderPath
$directory = Split-Path -Parent $launcher
if (-not (Test-Path -LiteralPath (Join-Path $directory 'MSBOOST.exe') -PathType Leaf)) { throw 'The launcher must have its existing MSBOOST.exe beside it.' }
if ($launcher.Contains('"') -or $launcher.Contains('%')) { throw 'This smoke runner requires a launcher path without quote or percent characters.' }

function Get-SmokeStateSnapshot {
    $snapshot = foreach ($name in @('msboost-state.dat', 'msboost-current-config.txt')) {
        $path = Join-Path $directory $name
        $exists = Test-Path -LiteralPath $path -PathType Leaf
        $hash = if ($exists) { (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash } else { '' }
        [pscustomobject]@{ Name = $name; Exists = $exists; SHA256 = $hash }
    }
    return ($snapshot | ConvertTo-Json -Compress)
}
function Get-SmokeProcessSnapshot {
    return [string]::Join(',', [string[]]@(Get-Process -Name MSBOOST -ErrorAction SilentlyContinue | Sort-Object Id | Select-Object -ExpandProperty Id))
}

$beforeState = Get-SmokeStateSnapshot
$beforeProcesses = Get-SmokeProcessSnapshot
$start = New-Object Diagnostics.ProcessStartInfo
$start.FileName = Join-Path $env:SystemRoot 'System32\cmd.exe'
$start.Arguments = '/d /c ""' + $launcher + '""'
$start.WorkingDirectory = $directory
$start.UseShellExecute = $false
$start.CreateNoWindow = $true
$start.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
$start.RedirectStandardInput = $true
$start.RedirectStandardOutput = $true
$start.RedirectStandardError = $true
$start.StandardOutputEncoding = New-Object Text.UTF8Encoding($false)
$start.StandardErrorEncoding = New-Object Text.UTF8Encoding($false)
$process = New-Object Diagnostics.Process
$process.StartInfo = $start
$started = $false
$exitCode = $null
$stdout = ''
$stderr = ''
$failure = $null
try {
    if (-not $process.Start()) { throw 'The owned BAT wrapper process could not start.' }
    $started = $true
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    $process.StandardInput.WriteLine('Q')
    $process.StandardInput.WriteLine('Y')
    $process.StandardInput.Close()
    if (-not $process.WaitForExit($TimeoutMs)) {
        throw ('The owned BAT wrapper exceeded its {0} ms deadline.' -f $TimeoutMs)
    }
    $exitCode = $process.ExitCode
    $stdout = $stdoutTask.Result
    $stderr = $stderrTask.Result
} catch {
    $failure = $_.Exception.Message
} finally {
    if ($started -and -not $process.HasExited) {
        # Kill only this test-created cmd tree, including its own PowerShell child.
        # Existing MSBOOST processes cannot be descendants of this fresh wrapper.
        $terminate = New-Object Diagnostics.ProcessStartInfo
        $terminate.FileName = Join-Path $env:SystemRoot 'System32\taskkill.exe'
        $terminate.Arguments = '/PID ' + $process.Id + ' /T /F'
        $terminate.UseShellExecute = $false
        $terminate.CreateNoWindow = $true
        $terminate.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
        $terminate.RedirectStandardOutput = $true
        $terminate.RedirectStandardError = $true
        $killer = [Diagnostics.Process]::Start($terminate)
        try { [void]$killer.WaitForExit(5000) } finally { $killer.Dispose() }
        [void]$process.WaitForExit(5000)
    }
    $process.Dispose()
}

$afterState = Get-SmokeStateSnapshot
$afterProcesses = Get-SmokeProcessSnapshot
if ($beforeState -cne $afterState) { throw 'The launcher state files changed during the Q/Y smoke test.' }
if ($beforeProcesses -cne $afterProcesses) { throw ('Existing MSBOOST process IDs changed: before [{0}], after [{1}].' -f $beforeProcesses, $afterProcesses) }
if ($null -ne $failure) { throw $failure }
if ($exitCode -ne 0) { throw ('BAT wrapper exited with code {0}. {1}' -f $exitCode, $stderr) }
foreach ($entry in @('[0] 加载配置文件', '[1] 启动MSBOOST', '[2] 停止MSBOOST', '[3] 检查MSBOOST IP 地址', '[Q] 退出启动器')) {
    if (-not $stdout.Contains($entry)) { throw ('Main menu entry was not rendered: ' + $entry) }
}
# ConsoleHost omits Read-Host prompt text when stdin is redirected, but preserves
# the exit-page header and the echoed Q/Y inputs.
if ($stdout -notmatch '(?m)^\s*退出启动器\s*$' -or $stdout -notmatch '(?m)^\s*Q\s*$' -or $stdout -notmatch '(?m)^\s*Y\s*$') {
    throw 'The Q/Y exit confirmation was not reached.'
}
if ($stdout -match '正在查询出口|正在测量 TCP|正在启动|正在停止|正在加载：') { throw 'The smoke test unexpectedly entered an operational menu action.' }

Write-Host 'PASS hidden cmd /d /c wrapper entered the 0/1/2/3/Q main menu and exited with Q/Y.'
Write-Host 'PASS exit code 0; no IP query, latency probe, start, stop, or config replacement was requested.'
Write-Host ('PASS existing MSBOOST process IDs unchanged: [{0}].' -f $afterProcesses)
Write-Host 'PASS both launcher state-file existence and SHA256 snapshots are unchanged.'
Write-Host ('PowerShell {0}' -f $PSVersionTable.PSVersion)
