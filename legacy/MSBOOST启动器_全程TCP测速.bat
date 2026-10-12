@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "MSBOOST_SELF=%~f0"
set "MSBOOST_PS=%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe"
if exist "%SystemRoot%\Sysnative\WindowsPowerShell\v1.0\powershell.exe" set "MSBOOST_PS=%SystemRoot%\Sysnative\WindowsPowerShell\v1.0\powershell.exe"
"%MSBOOST_PS%" -NoLogo -NoProfile -ExecutionPolicy Bypass -Command "$p=$env:MSBOOST_SELF;$u=New-Object System.Text.UTF8Encoding($false);$s=[IO.File]::ReadAllText($p,$u);$m='#'+'<'+'MSBOOST_PS'+'>';$i=$s.IndexOf($m,[StringComparison]::Ordinal);if($i -lt 0){exit 97};& ([ScriptBlock]::Create($s.Substring($i+$m.Length)))"
set "MSBOOST_RC=%ERRORLEVEL%"
endlocal & exit /b %MSBOOST_RC%
#<MSBOOST_PS>

$script:Utf8 = New-Object System.Text.UTF8Encoding($false)
$script:OriginalForeground = [ConsoleColor]::White
$script:OriginalBackground = [ConsoleColor]::Black
try {
    $script:OriginalForeground = $Host.UI.RawUI.ForegroundColor
    $script:OriginalBackground = $Host.UI.RawUI.BackgroundColor
    [Console]::InputEncoding = $script:Utf8
    [Console]::OutputEncoding = $script:Utf8
    $OutputEncoding = $script:Utf8
    $Host.UI.RawUI.ForegroundColor = [ConsoleColor]::White
    $Host.UI.RawUI.BackgroundColor = [ConsoleColor]::Black
    $Host.UI.RawUI.WindowTitle = 'MSBOOST启动器'
} catch {
}

if ($PSVersionTable.PSVersion.Major -lt 3) {
    try { $Host.UI.RawUI.ForegroundColor = [ConsoleColor]::Red } catch {}
    Clear-Host
    Write-Host ''
    Write-Host '  ===================================================================='
    Write-Host '                         MSBOOST启动器错误'
    Write-Host '  ===================================================================='
    Write-Host ''
    Write-Host '  [错误] 当前 Windows PowerShell 版本过低。'
    Write-Host ('  当前版本：{0}' -f $PSVersionTable.PSVersion)
    Write-Host '  本启动器需要 Windows PowerShell 3.0 或更高版本。'
    Write-Host ''
    [void](Read-Host '  按回车键退出')
    try {
        $Host.UI.RawUI.ForegroundColor = $script:OriginalForeground
        $Host.UI.RawUI.BackgroundColor = $script:OriginalBackground
    } catch {}
    exit 96
}

$script:Root = [IO.Path]::GetDirectoryName($env:MSBOOST_SELF)
$script:ExePath = Join-Path $script:Root 'msboost.exe'
$script:CurlPath = Join-Path $env:SystemRoot 'System32\curl.exe'
if (-not (Test-Path -LiteralPath $script:CurlPath -PathType Leaf)) {
    $script:CurlPath = Join-Path $script:Root 'curl.exe'
}
$script:StatePath = Join-Path $script:Root 'msboost-state.dat'
$script:LegacyStatePath = Join-Path $script:Root 'msboost-current-config.txt'
$script:ErrorLogPath = Join-Path $script:Root 'msboost-last-error.log'
$script:IpCheckUrl = 'https://api-ipv4.ip.sb/ip'
# 游戏目标；全程测速需要节点按《MSBOOST全程TCP测速整改计划.md》提供版本 2 协议。
$script:GameAddress = '35.155.204.207'
$script:GamePort = 8585
# 这是通过 SOCKS 访问的远端回环端口，不是本地 Windows 监听端口。
$script:FullPathProbePort = 20424
$script:FullPathWarmupTimeoutMs = 5000
$script:LatencySampleCount = 3
$script:LatencyTimeoutMs = 2000
$script:DefaultPort = 1080
Set-Location -LiteralPath $script:Root

function Write-Header {
    param(
        [string]$Title,
        [ConsoleColor]$Color = [ConsoleColor]::White
    )

    Write-Host ''
    Write-Host ('  ' + ('=' * 68)) -ForegroundColor $Color
    Write-Host ('                         ' + $Title) -ForegroundColor $Color
    Write-Host ('  ' + ('=' * 68)) -ForegroundColor $Color
    Write-Host ''
}

function Set-ScreenColor {
    param([ConsoleColor]$Color = [ConsoleColor]::White)
    try {
        $Host.UI.RawUI.ForegroundColor = $Color
        $Host.UI.RawUI.BackgroundColor = [ConsoleColor]::Black
    } catch {
    }
}

function Restore-ConsoleColors {
    try {
        $Host.UI.RawUI.ForegroundColor = $script:OriginalForeground
        $Host.UI.RawUI.BackgroundColor = $script:OriginalBackground
    } catch {
    }
}

function Start-ErrorPage {
    param([string]$Title)
    Set-ScreenColor -Color Red
    Clear-Host
    Write-Header -Title $Title -Color Red
}

function Wait-ForMenu {
    Write-Host ''
    [void](Read-Host '  按回车键返回主菜单')
}

function Normalize-Port {
    param(
        $Value,
        [int]$Fallback = 1080
    )

    $port = 0
    if ([int]::TryParse([string]$Value, [ref]$port) -and $port -ge 1 -and $port -le 65535) {
        return $port
    }
    return $Fallback
}

function Get-PortFromJsonFile {
    param(
        [string]$Path,
        [int]$Fallback
    )

    try {
        $jsonText = [IO.File]::ReadAllText($Path, $script:Utf8)
        $json = $jsonText | ConvertFrom-Json
        return (Normalize-Port -Value $json.socks5Port -Fallback $Fallback)
    } catch {
        return $Fallback
    }
}

function New-DefaultState {
    return [pscustomobject]@{
        ConfigFile = '未加载'
        Socks5Port = $script:DefaultPort
    }
}

function Load-State {
    $state = New-DefaultState

    if (Test-Path -LiteralPath $script:StatePath -PathType Leaf) {
        try {
            $savedText = [IO.File]::ReadAllText($script:StatePath, $script:Utf8)
            $saved = $savedText | ConvertFrom-Json
            if (-not [string]::IsNullOrWhiteSpace([string]$saved.ConfigFile)) {
                $state.ConfigFile = [string]$saved.ConfigFile
                if ($state.ConfigFile -eq '未记录') {
                    $state.ConfigFile = '未加载'
                }
            }
            $state.Socks5Port = Normalize-Port -Value $saved.Socks5Port -Fallback $script:DefaultPort
            return $state
        } catch {
        }
    }

    if (Test-Path -LiteralPath $script:LegacyStatePath -PathType Leaf) {
        try {
            $legacyName = [IO.File]::ReadAllText($script:LegacyStatePath, $script:Utf8).Trim()
            if (-not [string]::IsNullOrWhiteSpace($legacyName)) {
                if ($legacyName -eq '未记录') {
                    $state.ConfigFile = '未加载'
                } else {
                    $state.ConfigFile = $legacyName
                    $legacyConfigPath = Join-Path $script:Root $legacyName
                    if (Test-Path -LiteralPath $legacyConfigPath -PathType Leaf) {
                        $state.Socks5Port = Get-PortFromJsonFile -Path $legacyConfigPath -Fallback $script:DefaultPort
                    }
                }
            }
        } catch {
        }
    }

    return $state
}

function Save-State {
    try {
        $data = @{
            ConfigFile = $script:State.ConfigFile
            Socks5Port = $script:State.Socks5Port
        }
        $json = $data | ConvertTo-Json -Compress
        [IO.File]::WriteAllText($script:StatePath, $json, $script:Utf8)
        return $true
    } catch {
        return $false
    }
}

function Invoke-Captured {
    param([string[]]$Arguments)

    $output = @()
    $code = 1
    try {
        $output = @(& $script:ExePath @Arguments 2>&1 | ForEach-Object { [string]$_ })
        if ($null -ne $LASTEXITCODE) {
            $code = [int]$LASTEXITCODE
        }
    } catch {
        $output = @($_.Exception.Message)
    }

    return [pscustomobject]@{
        ExitCode = $code
        Output = $output
    }
}

function Invoke-Silent {
    param([string[]]$Arguments)

    if ($Arguments.Count -ne 1 -or @('start', 'stop') -notcontains $Arguments[0]) {
        return 2
    }

    $process = $null
    try {
        $startInfo = New-Object System.Diagnostics.ProcessStartInfo
        $startInfo.FileName = $script:ExePath
        $startInfo.Arguments = $Arguments[0]
        $startInfo.UseShellExecute = $false
        $startInfo.CreateNoWindow = $true
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true

        $process = New-Object System.Diagnostics.Process
        $process.StartInfo = $startInfo
        if (-not $process.Start()) {
            return 1
        }
        if (-not $process.WaitForExit(15000)) {
            try { $process.Kill() } catch {}
            return 124
        }
        return [int]$process.ExitCode
    } catch {
        return 1
    } finally {
        if ($null -ne $process) {
            $process.Dispose()
        }
    }
}

function Save-ErrorDetails {
    param(
        [string]$Action,
        [int]$ExitCode,
        [object[]]$Output
    )

    try {
        $lines = @(
            'MSBOOST启动器错误记录'
            ('时间：{0}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'))
            ('操作：{0}' -f $Action)
            ('返回代码：{0}' -f $ExitCode)
            '原程序信息：'
        ) + @($Output | ForEach-Object { [string]$_ })
        [IO.File]::WriteAllLines($script:ErrorLogPath, [string[]]$lines, $script:Utf8)
        return $true
    } catch {
        return $false
    }
}

function Show-NativeError {
    param(
        [string]$Action,
        [int]$ExitCode,
        [object[]]$Output
    )

    $text = [string]::Join(' ', [string[]]@($Output))
    Start-ErrorPage -Title ('{0}失败' -f $Action)
    Write-Host ('  [失败] {0}失败。' -f $Action)

    if ($text -match 'config file.*(doesn.t exist|not exist)') {
        Write-Host '  原因：尚未加载有效的客户端配置。'
    } elseif ($text -match 'client is not running') {
        Write-Host '  原因：MSBOOST当前未运行。'
    } elseif ($text -match 'address already in use|bind') {
        Write-Host '  原因：本地端口已被其他程序占用。'
    } elseif ($text -match 'unknown field|unmarshal|validate|invalid') {
        Write-Host '  原因：配置内容无效，或该文件不是有效的MSBOOST客户端配置。'
    } else {
        Write-Host ('  程序返回代码：{0}' -f $ExitCode)
    }

    if (Save-ErrorDetails -Action $Action -ExitCode $ExitCode -Output $Output) {
        Write-Host '  详细信息已保存到：msboost-last-error.log'
    }
}

function Get-EffectiveConfig {
    $described = Invoke-Captured -Arguments @('describe', 'config')
    if ($described.ExitCode -eq 0 -and $described.Output.Count -gt 0) {
        try {
            $text = [string]::Join([Environment]::NewLine, [string[]]$described.Output)
            $firstBrace = $text.IndexOf('{')
            $lastBrace = $text.LastIndexOf('}')
            if ($firstBrace -ge 0 -and $lastBrace -gt $firstBrace) {
                $jsonText = $text.Substring($firstBrace, $lastBrace - $firstBrace + 1)
                return ($jsonText | ConvertFrom-Json)
            }
        } catch {
        }
    }
    return $null
}

function Get-EffectivePort {
    param(
        [string]$SelectedConfigPath,
        [int]$Fallback
    )

    $config = Get-EffectiveConfig
    $port = Normalize-Port -Value $config.socks5Port -Fallback 0
    if ($port -gt 0) {
        return $port
    }

    return (Get-PortFromJsonFile -Path $SelectedConfigPath -Fallback $Fallback)
}

function Sync-EffectivePort {
    $selectedPath = Join-Path $script:Root $script:State.ConfigFile
    $port = Get-EffectivePort -SelectedConfigPath $selectedPath -Fallback $script:State.Socks5Port
    if ($port -ne $script:State.Socks5Port) {
        $script:State.Socks5Port = $port
        [void](Save-State)
    }
    return $port
}

function Get-LatencyTargets {
    param($Config)

    $targets = [pscustomobject]@{
        RelayAddress = ''; RelayPort = 0
        RelayError = '无法唯一确定测试目标'
        GameError = ''
        Socks5Port = 0
    }
    if ($null -eq $Config) { return $targets }
    $targets.Socks5Port = Normalize-Port -Value $Config.socks5Port -Fallback 0
    if (@($Config.socks5Authentication | Where-Object { $null -ne $_ }).Count -gt 0) {
        $targets.GameError = '本地 SOCKS5 需要认证，当前测速不支持'
    }
    if ([string]::IsNullOrWhiteSpace([string]$Config.activeProfile)) { return $targets }
    $profiles = @($Config.profiles | Where-Object { $_.profileName -ceq $Config.activeProfile })
    if ($profiles.Count -ne 1) { return $targets }
    $profile = $profiles[0]

    $servers = @($profile.servers | Where-Object { $null -ne $_ })
    $bindings = @($servers | ForEach-Object { $_.portBindings } |
        Where-Object { $_.protocol -eq 'TCP' })
    if ($bindings.Count -eq 0) {
        if (@($servers | ForEach-Object { $_.portBindings } |
                Where-Object { $_.protocol -eq 'UDP' }).Count -gt 0) {
            $targets.RelayError = '无 TCP 测试端口'
        }
        return $targets
    }
    if ($servers.Count -ne 1 -or $bindings.Count -ne 1 -or
        -not [string]::IsNullOrWhiteSpace([string]$bindings[0].portRange)) {
        return $targets
    }
    $port = Normalize-Port -Value $bindings[0].port -Fallback 0
    $address = [string]$servers[0].domainName
    if ([string]::IsNullOrWhiteSpace($address)) { $address = [string]$servers[0].ipAddress }
    $address = $address.Trim()
    if ($port -eq 0 -or [string]::IsNullOrWhiteSpace($address)) { return $targets }
    $targets.RelayAddress = $address
    $targets.RelayPort = $port
    $targets.RelayError = ''
    return $targets
}

function Get-ProbeRemaining {
    param([Diagnostics.Stopwatch]$Watch, [int]$TimeoutMs)
    $remaining = $TimeoutMs - $Watch.Elapsed.TotalMilliseconds
    if ($remaining -le 0) { throw (New-Object TimeoutException('连接超时')) }
    return [int][Math]::Ceiling($remaining)
}

function Get-RelayResolvedAddresses {
    param([string]$Address, [int]$TimeoutMs = 2000)
    $waitHandle = $null
    try {
        $pending = [Net.Dns]::BeginGetHostAddresses($Address, $null, $null)
        $waitHandle = $pending.AsyncWaitHandle
        if (-not $waitHandle.WaitOne($TimeoutMs, $false)) {
            throw (New-Object TimeoutException('中转域名解析超时'))
        }
        return @([Net.Dns]::EndGetHostAddresses($pending))
    } finally {
        if ($null -ne $waitHandle) { try { $waitHandle.Close() } catch {} }
    }
}

function Resolve-LatencyRelayAddress {
    param([string]$Address, [int]$TimeoutMs = 2000)
    $parsed = $null
    if ([Net.IPAddress]::TryParse($Address, [ref]$parsed)) {
        if ($parsed.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork) {
            return [pscustomobject]@{ Address = $parsed.ToString(); Error = '' }
        }
        return [pscustomobject]@{ Address = ''; Error = '当前节点测速仅支持 IPv4' }
    }
    try {
        $addresses = @(Get-RelayResolvedAddresses -Address $Address -TimeoutMs $TimeoutMs)
        $ipv4 = @($addresses | Where-Object { $_.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork } |
            ForEach-Object { $_.ToString() } | Sort-Object -Unique)
        if ($ipv4.Count -eq 0) { throw (New-Object IO.IOException('中转域名未解析到 IPv4 地址')) }
        if ($ipv4.Count -ne 1) { throw (New-Object IO.IOException('中转域名对应多个 IPv4，无法唯一确定测试目标')) }
        return [pscustomobject]@{ Address = $ipv4[0]; Error = '' }
    } catch {
        $exception = $_.Exception
        while ($null -ne $exception.InnerException) { $exception = $exception.InnerException }
        $reason = if ($exception -is [Net.Sockets.SocketException]) { '中转域名解析失败' } else { $exception.Message }
        return [pscustomobject]@{ Address = ''; Error = $reason }
    }
}

function Read-ProbeBytes {
    param($Stream, [int]$Count, [Diagnostics.Stopwatch]$Watch, [int]$TimeoutMs, [string]$PeerLabel = '代理')
    $buffer = New-Object byte[] $Count
    $offset = 0
    while ($offset -lt $Count) {
        $Stream.ReadTimeout = Get-ProbeRemaining -Watch $Watch -TimeoutMs $TimeoutMs
        $read = $Stream.Read($buffer, $offset, $Count - $offset)
        if ($read -eq 0) { throw (New-Object IO.IOException($PeerLabel + '提前关闭连接')) }
        $offset += $read
    }
    [void](Get-ProbeRemaining -Watch $Watch -TimeoutMs $TimeoutMs)
    return ,$buffer
}

function Write-ProbeBytes {
    param($Stream, [byte[]]$Bytes, [Diagnostics.Stopwatch]$Watch, [int]$TimeoutMs)
    $Stream.WriteTimeout = Get-ProbeRemaining -Watch $Watch -TimeoutMs $TimeoutMs
    $Stream.Write($Bytes, 0, $Bytes.Length)
}

function Initialize-Socks5Connection {
    param($Stream, [string]$Address, [int]$Port, [Diagnostics.Stopwatch]$Watch, [int]$TimeoutMs)

    Write-ProbeBytes $Stream ([byte[]]@(5, 1, 0)) $Watch $TimeoutMs
    $greeting = Read-ProbeBytes $Stream 2 $Watch $TimeoutMs
    if ($greeting[0] -ne 5) { throw (New-Object IO.IOException('SOCKS5 协议回复无效')) }
    if ($greeting[1] -eq 2 -or $greeting[1] -eq 255) {
        throw (New-Object IO.IOException('本地 SOCKS5 需要认证或不接受无认证连接'))
    }
    if ($greeting[1] -ne 0) { throw (New-Object IO.IOException('SOCKS5 认证方式不支持')) }
    $targetIp = $null
    if ([Net.IPAddress]::TryParse($Address, [ref]$targetIp)) {
        $type = if ($targetIp.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork) { 1 } else { 4 }
        [byte[]]$request = @([byte]5, [byte]1, [byte]0, [byte]$type) + $targetIp.GetAddressBytes()
    } else {
        $asciiName = (New-Object Globalization.IdnMapping).GetAscii($Address)
        [byte[]]$nameBytes = [Text.Encoding]::ASCII.GetBytes($asciiName)
        if ($nameBytes.Length -lt 1 -or $nameBytes.Length -gt 255) {
            throw (New-Object ArgumentException('测试域名长度无效'))
        }
        [byte[]]$request = @([byte]5, [byte]1, [byte]0, [byte]3, [byte]$nameBytes.Length) + $nameBytes
    }
    $request += [byte[]]@([byte]($Port -shr 8), [byte]($Port -band 255))
    Write-ProbeBytes $Stream $request $Watch $TimeoutMs
    $reply = Read-ProbeBytes $Stream 4 $Watch $TimeoutMs
    if ($reply[0] -ne 5 -or $reply[2] -ne 0) { throw (New-Object IO.IOException('SOCKS5 协议回复无效')) }
    switch ($reply[3]) {
        1 { $tailLength = 6 }
        4 { $tailLength = 18 }
        3 {
            $length = Read-ProbeBytes $Stream 1 $Watch $TimeoutMs
            if ($length[0] -eq 0) { throw (New-Object IO.IOException('SOCKS5 绑定地址无效')) }
            $tailLength = [int]$length[0] + 2
        }
        default { throw (New-Object IO.IOException('SOCKS5 地址类型不支持')) }
    }
    [void](Read-ProbeBytes $Stream $tailLength $Watch $TimeoutMs)
    if ($reply[1] -ne 0) {
        $reason = switch ($reply[1]) {
            1 { '代理连接失败' }
            2 { '代理规则禁止连接' }
            3 { '目标网络不可达' }
            4 { '目标主机不可达' }
            5 { '目标拒绝连接' }
            6 { '代理连接超时' }
            7 { '代理不支持 CONNECT 命令' }
            8 { '代理不支持目标地址类型' }
            default { 'SOCKS5 返回未知错误' }
        }
        throw (New-Object IO.IOException($reason))
    }
}

function New-LatencyStats {
    param([object[]]$Samples)
    $successCount = 0
    $sum = 0.0
    $errors = @()
    foreach ($sample in $Samples) {
        if ($sample.Success) { $successCount++; $sum += $sample.Milliseconds }
        else { $errors += $sample.Error }
    }
    $average = $null
    if ($successCount -gt 0) { $average = $sum / $successCount }
    return [pscustomobject]@{
        SuccessCount = $successCount; TotalCount = $Samples.Count; AverageMs = $average
        Errors = @($errors | Select-Object -Unique); Samples = @($Samples)
    }
}

function New-UnavailableLatencyResult {
    param([string]$Message)
    return [pscustomobject]@{
        SuccessCount = 0; TotalCount = 3; AverageMs = $null; Errors = @($Message); Samples = @()
    }
}

function Read-FullPathLine {
    param($Stream, [Diagnostics.Stopwatch]$Watch, [int]$TimeoutMs)
    $data = New-Object IO.MemoryStream
    $buffer = New-Object byte[] 512
    try {
        while ($true) {
            $Stream.ReadTimeout = Get-ProbeRemaining $Watch $TimeoutMs
            $read = $Stream.Read($buffer, 0, [Math]::Min($buffer.Length, 4097 - [int]$data.Length))
            # Capture the arrival time before copying, decoding or validating the reply.
            $receivedMs = $Watch.Elapsed.TotalMilliseconds
            if ($read -eq 0) { throw (New-Object IO.IOException('节点测速服务提前关闭连接')) }
            if ($receivedMs -gt $TimeoutMs) { throw (New-Object TimeoutException('测速请求超时')) }
            $newline = [Array]::IndexOf($buffer, [byte]10, 0, $read)
            if ($newline -ge 0) {
                if ($newline -ne $read - 1) { throw (New-Object IO.IOException('测速服务发送了未请求的额外数据')) }
                $data.Write($buffer, 0, $newline)
                if ($data.Length -gt 4096) { throw (New-Object IO.IOException('测速回复过长')) }
                return [pscustomobject]@{ Bytes = $data.ToArray(); Milliseconds = $receivedMs }
            }
            $data.Write($buffer, 0, $read)
            if ($data.Length -gt 4096) { throw (New-Object IO.IOException('测速回复过长')) }
        }
    } finally { $data.Dispose() }
}

function ConvertFrom-FullPathJson {
    param([byte[]]$Bytes)
    $json = (New-Object Text.UTF8Encoding($false, $true)).GetString($Bytes)
    # v2 is deliberately flat ASCII JSON. Check its grammar and unique keys before
    # ConvertFrom-Json, which otherwise accepts duplicate keys on some PS versions.
    $scalar = '(?:"[A-Za-z0-9_.-]*"|true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)'
    $field = '"[a-z_]+"[ \t\r]*:[ \t\r]*' + $scalar
    $pattern = '\A[ \t\r]*\{[ \t\r]*' + $field + '(?:[ \t\r]*,[ \t\r]*' + $field + ')*[ \t\r]*\}[ \t\r]*\z'
    if (-not [regex]::IsMatch($json, $pattern)) { throw (New-Object IO.IOException('测速回复不是有效的版本 2 扁平 JSON')) }
    $names = @{}
    foreach ($match in [regex]::Matches($json, '"([a-z_]+)"[ \t\r]*:')) {
        $name = $match.Groups[1].Value
        if ($names.ContainsKey($name)) { throw (New-Object IO.IOException('测速回复包含重复字段')) }
        $names[$name] = $true
    }
    return ($json | ConvertFrom-Json -ErrorAction Stop)
}

function Assert-FullPathReply {
    param($Reply, [string]$Operation, [string]$Nonce, [string]$NodeId = '', [string]$GameAddress = '', [int]$GamePort = 0)
    if ($Reply -isnot [pscustomobject] -or $Reply.version -isnot [int] -or $Reply.version -ne 2) {
        throw (New-Object IO.IOException('节点测速协议不兼容，需要版本 2'))
    }
    if ($Reply.operation -isnot [string] -or $Reply.operation -cne $Operation -or
        $Reply.nonce -isnot [string] -or $Reply.nonce -cne $Nonce -or
        $Reply.node_id -isnot [string] -or $Reply.node_id -cnotmatch '^[0-9a-f]{32}$' -or
        $Reply.success -isnot [bool]) {
        throw (New-Object IO.IOException('测速回复的操作、随机标识或节点身份无效'))
    }
    $expected = @('version', 'operation', 'nonce', 'node_id', 'success')
    if ($Operation -eq 'hello') {
        if (-not $Reply.success) { throw (New-Object IO.IOException('节点未完成版本 2 测速预热')) }
    } else {
        if ($Reply.node_id -cne $NodeId) { throw (New-Object IO.IOException('测速回复节点与本次预热不一致')) }
        if ($Reply.target_address -isnot [string] -or $Reply.target_address -cne $GameAddress -or
            $Reply.target_port -isnot [int] -or $Reply.target_port -ne $GamePort) {
            throw (New-Object IO.IOException('测速回复的游戏目标不匹配'))
        }
        $expected += @('target_address', 'target_port')
        if (-not $Reply.success) {
            $expected += 'error'
            if ($Reply.error -isnot [string] -or
                @('timeout', 'refused', 'unreachable', 'target_not_allowed', 'failed') -cnotcontains $Reply.error) {
                throw (New-Object IO.IOException('测速回复的错误类型无效'))
            }
        }
    }
    $actual = @($Reply.PSObject.Properties | ForEach-Object { $_.Name } | Sort-Object)
    if ([string]::Join('|', [string[]]$actual) -cne [string]::Join('|', [string[]]@($expected | Sort-Object))) {
        throw (New-Object IO.IOException('测速回复包含未知、缺失或冲突字段'))
    }
}

function Get-FullPathFailureReason {
    param($Exception)
    while ($null -ne $Exception.InnerException) { $Exception = $Exception.InnerException }
    if ($Exception -is [Net.Sockets.SocketException]) {
        switch ($Exception.SocketErrorCode.ToString()) {
            'ConnectionRefused' { return '连接被拒绝' }
            'TimedOut' { return '测速请求超时' }
            'ConnectionReset' { return '测速连接被重置' }
            'NetworkUnreachable' { return '目标网络不可达' }
            'HostUnreachable' { return '目标主机不可达' }
            default { return ('TCP 连接失败（{0}）' -f $Exception.SocketErrorCode) }
        }
    }
    if ($Exception -is [TimeoutException]) { return '测速请求超时' }
    if ($Exception -is [Text.DecoderFallbackException]) { return '测速回复不是有效 UTF-8' }
    return $Exception.Message
}

function Close-FullPathSession {
    param($Session)
    if ($null -ne $Session) {
        if ($null -ne $Session.Stream) { try { $Session.Stream.Dispose() } catch {} }
        if ($null -ne $Session.Client) { try { $Session.Client.Close() } catch {} }
    }
}

function Open-FullPathSession {
    param([int]$Socks5Port, [int]$ProbePort = 20424, [int]$TimeoutMs = 5000)
    $client = $null; $stream = $null; $waitHandle = $null
    $keep = $false
    $phase = 'proxy'
    $watch = [Diagnostics.Stopwatch]::StartNew()
    try {
        $client = New-Object Net.Sockets.TcpClient
        $client.NoDelay = $true
        $pending = $client.BeginConnect([Net.IPAddress]::Loopback, $Socks5Port, $null, $null)
        $waitHandle = $pending.AsyncWaitHandle
        if (-not $waitHandle.WaitOne((Get-ProbeRemaining $watch $TimeoutMs), $false)) {
            throw (New-Object TimeoutException('连接本地代理超时'))
        }
        $client.EndConnect($pending)
        $phase = 'socks'
        $stream = $client.GetStream()
        # This SOCKS destination is resolved by the remote MSBOOST node, not locally.
        Initialize-Socks5Connection $stream '127.0.0.1' $ProbePort $watch $TimeoutMs
        $phase = 'hello'
        $nonce = [guid]::NewGuid().ToString('N')
        $request = @{ version = 2; operation = 'hello'; nonce = $nonce } | ConvertTo-Json -Compress
        Write-ProbeBytes $stream ([Text.Encoding]::UTF8.GetBytes($request + "`n")) $watch $TimeoutMs
        $line = Read-FullPathLine $stream $watch $TimeoutMs
        $reply = ConvertFrom-FullPathJson $line.Bytes
        Assert-FullPathReply -Reply $reply -Operation 'hello' -Nonce $nonce
        [void](Get-ProbeRemaining $watch $TimeoutMs)
        $keep = $true
        return [pscustomobject]@{ Client = $client; Stream = $stream; NodeId = $reply.node_id }
    } catch {
        $reason = Get-FullPathFailureReason $_.Exception
        if ($phase -eq 'proxy') {
            throw (New-Object IO.IOException('本地代理未启动、端口未监听或连接超时：' + $reason))
        }
        if ($phase -eq 'socks' -and $reason -match '认证') { throw (New-Object IO.IOException($reason)) }
        throw (New-Object IO.IOException('节点未提供兼容的版本 2 全程 TCP 测速服务或服务不可达：' + $reason))
    } finally {
        $watch.Stop()
        if ($null -ne $waitHandle) { try { $waitHandle.Close() } catch {} }
        if (-not $keep) {
            if ($null -ne $stream) { try { $stream.Dispose() } catch {} }
            if ($null -ne $client) { try { $client.Close() } catch {} }
        }
    }
}

function Invoke-FullPathTcpSamples {
    param([int]$Socks5Port, [string]$GameAddress, [int]$GamePort,
        [int]$Count = 3, [int]$TimeoutMs = 2000, [int]$WarmupTimeoutMs = 5000)
    $parsed = $null
    if ($Socks5Port -lt 1 -or $Socks5Port -gt 65535 -or $GamePort -lt 1 -or $GamePort -gt 65535 -or
        $Count -lt 1 -or $Count -gt 3 -or $TimeoutMs -lt 1 -or $TimeoutMs -gt 2000 -or
        $WarmupTimeoutMs -lt 1 -or $WarmupTimeoutMs -gt 5000 -or
        -not [Net.IPAddress]::TryParse($GameAddress, [ref]$parsed) -or
        $parsed.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or $parsed.ToString() -cne $GameAddress) {
        return (New-UnavailableLatencyResult '测速参数无效，请使用规范 IPv4、有效端口及超时')
    }
    $probePort = 20424
    if ($script:FullPathProbePort -ge 1 -and $script:FullPathProbePort -le 65535) { $probePort = [int]$script:FullPathProbePort }
    $session = $null
    $samples = @()
    try {
        for ($index = 0; $index -lt $Count; $index++) {
            if ($null -eq $session) {
                try { $session = Open-FullPathSession -Socks5Port $Socks5Port -ProbePort $probePort -TimeoutMs $WarmupTimeoutMs }
                catch {
                    $reason = Get-FullPathFailureReason $_.Exception
                    # A missing/old service must not cause repeated connection attempts.
                    for ($missing = $index; $missing -lt $Count; $missing++) {
                        $samples += [pscustomobject]@{ Success = $false; Milliseconds = $null; Error = $reason }
                    }
                    break
                }
            }
            $nonce = [guid]::NewGuid().ToString('N')
            $request = @{ version = 2; operation = 'connect'; nonce = $nonce; target_address = $GameAddress; target_port = $GamePort } | ConvertTo-Json -Compress
            $bytes = [Text.Encoding]::UTF8.GetBytes($request + "`n")
            # All connection setup, warmup and request serialization are outside timing.
            $watch = [Diagnostics.Stopwatch]::StartNew()
            try {
                Write-ProbeBytes $session.Stream $bytes $watch $TimeoutMs
                $line = Read-FullPathLine $session.Stream $watch $TimeoutMs
                $watch.Stop()
                $reply = ConvertFrom-FullPathJson $line.Bytes
                Assert-FullPathReply -Reply $reply -Operation 'connect' -Nonce $nonce -NodeId $session.NodeId -GameAddress $GameAddress -GamePort $GamePort
                if ($reply.success) {
                    $samples += [pscustomobject]@{ Success = $true; Milliseconds = [double]$line.Milliseconds; Error = '' }
                } else {
                    $reason = switch -CaseSensitive ($reply.error) {
                        'timeout' { '节点到游戏 TCP 连接超时' }
                        'refused' { '游戏端口拒绝连接' }
                        'unreachable' { '节点无法到达游戏服务器' }
                        'target_not_allowed' { '节点不允许该目标，请使用公网 IPv4 和有效端口' }
                        'failed' { '节点到游戏 TCP 连接失败' }
                    }
                    $samples += [pscustomobject]@{ Success = $false; Milliseconds = $null; Error = $reason }
                }
            } catch {
                $samples += [pscustomobject]@{ Success = $false; Milliseconds = $null; Error = (Get-FullPathFailureReason $_.Exception) }
                Close-FullPathSession $session
                $session = $null
            } finally { $watch.Stop() }
        }
    } finally { Close-FullPathSession $session }
    return (New-LatencyStats -Samples $samples)
}

function Measure-TcpLatency {
    param(
        [string]$Address,
        [int]$Port,
        [int]$TimeoutMs = 2000
    )

    $client = $null
    $waitHandle = $null
    $dnsHandle = $null
    $watch = [Diagnostics.Stopwatch]::StartNew()
    $connectWatch = $null
    try {
        if ($TimeoutMs -le 0 -or $Port -lt 1 -or $Port -gt 65535 -or
            [string]::IsNullOrWhiteSpace($Address)) {
            throw (New-Object ArgumentException('测试地址、端口或超时无效'))
        }
        $connectAddress = $null
        $connectPort = $Port
        if (-not [Net.IPAddress]::TryParse($Address, [ref]$connectAddress)) {
            # DNS shares the attempt deadline but is excluded from TCP connection timing.
            $dnsResult = [Net.Dns]::BeginGetHostAddresses($Address, $null, $null)
            $dnsHandle = $dnsResult.AsyncWaitHandle
            if (-not $dnsHandle.WaitOne((Get-ProbeRemaining $watch $TimeoutMs), $false)) {
                throw (New-Object TimeoutException('域名解析超时'))
            }
            $addresses = @([Net.Dns]::EndGetHostAddresses($dnsResult))
            $ipv4 = @($addresses | Where-Object { $_.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork })
            if ($ipv4.Count -gt 0) { $connectAddress = $ipv4[0] }
            elseif ($addresses.Count -gt 0) { $connectAddress = $addresses[0] }
            else { throw (New-Object IO.IOException('域名解析失败')) }
        }

        $client = New-Object Net.Sockets.TcpClient($connectAddress.AddressFamily)
        $client.NoDelay = $true
        $connectWatch = [Diagnostics.Stopwatch]::StartNew()
        $asyncResult = $client.BeginConnect($connectAddress, $connectPort, $null, $null)
        $waitHandle = $asyncResult.AsyncWaitHandle
        if (-not $waitHandle.WaitOne((Get-ProbeRemaining $watch $TimeoutMs), $false)) {
            throw (New-Object TimeoutException('连接超时'))
        }
        $client.EndConnect($asyncResult)
        $connectWatch.Stop()
        [void](Get-ProbeRemaining $watch $TimeoutMs)
        return [pscustomobject]@{ Success = $true; Milliseconds = $connectWatch.Elapsed.TotalMilliseconds; Error = '' }
    } catch {
        $exception = $_.Exception
        while ($null -ne $exception.InnerException) { $exception = $exception.InnerException }
        if ($exception -is [Net.Sockets.SocketException]) {
            $reason = switch ($exception.SocketErrorCode.ToString()) {
                'ConnectionRefused' {
                    '目标拒绝连接'
                }
                'TimedOut' { '连接超时' }
                'HostNotFound' { '域名解析失败' }
                'NoData' { '域名解析失败' }
                'NetworkUnreachable' { '目标网络不可达' }
                'HostUnreachable' { '目标主机不可达' }
                'ConnectionReset' { '连接被重置' }
                default { 'TCP 连接失败（{0}）' -f $exception.SocketErrorCode }
            }
        } elseif ($exception -is [TimeoutException] -or $watch.Elapsed.TotalMilliseconds -ge $TimeoutMs) {
            $reason = '连接超时（{0} ms）' -f $TimeoutMs
        } else {
            $reason = $exception.Message
        }
        return [pscustomobject]@{ Success = $false; Milliseconds = $null; Error = $reason }
    } finally {
        if ($null -ne $client) { try { $client.Close() } catch {} }
        if ($null -ne $waitHandle) { try { $waitHandle.Close() } catch {} }
        if ($null -ne $dnsHandle) { try { $dnsHandle.Close() } catch {} }
        $watch.Stop()
    }
}

function Measure-LatencySamples {
    param([string]$Address, [int]$Port, [int]$Count = 3, [int]$TimeoutMs = 2000)
    $samples = @()
    for ($index = 0; $index -lt $Count; $index++) {
        $samples += Measure-TcpLatency -Address $Address -Port $Port -TimeoutMs $TimeoutMs
    }
    return (New-LatencyStats -Samples $samples)
}

function Format-LatencyResult {
    param($Result, [switch]$Compact)
    if ($Compact) {
        if ($Result.SuccessCount -gt 0) {
            $ms = ([double]$Result.AverageMs).ToString('F0', [Globalization.CultureInfo]::InvariantCulture)
            if ($Result.AverageMs -lt 0.5) { $ms = '<1' }
            return ($ms + ' ms')
        }
        return ('未测得：' + [string]::Join('、', [string[]]$Result.Errors))
    }
    $counts = '成功 {0}/{1}' -f $Result.SuccessCount, $Result.TotalCount
    if ($Result.SuccessCount -gt 0) {
        $ms = ([double]$Result.AverageMs).ToString('F1', [Globalization.CultureInfo]::InvariantCulture)
        if ($Result.AverageMs -lt 0.05) { $ms = '<0.1' }
        $text = '{0} ms（{1}）' -f $ms, $counts
        if ($Result.SuccessCount -lt $Result.TotalCount) {
            $text += '；部分失败：' + [string]::Join('、', [string[]]$Result.Errors)
        }
        return $text
    }
    return ('未测得（{0}；{1}）' -f $counts, [string]::Join('、', [string[]]$Result.Errors))
}

function Test-Socks5Ready {
    param(
        [int]$Port,
        [int]$TimeoutMs = 250
    )

    if ($Port -lt 1 -or $Port -gt 65535) {
        return $false
    }

    $client = New-Object System.Net.Sockets.TcpClient
    $stream = $null
    $waitHandle = $null
    try {
        $asyncResult = $client.BeginConnect(
            [Net.IPAddress]::Loopback,
            $Port,
            $null,
            $null
        )
        $waitHandle = $asyncResult.AsyncWaitHandle
        if (-not $waitHandle.WaitOne($TimeoutMs, $false)) {
            return $false
        }

        $client.EndConnect($asyncResult)
        if (-not $client.Connected) {
            return $false
        }

        $stream = $client.GetStream()
        $stream.ReadTimeout = $TimeoutMs
        $stream.WriteTimeout = $TimeoutMs
        [byte[]]$greeting = @(5, 2, 0, 2)
        $stream.Write($greeting, 0, $greeting.Length)
        $stream.Flush()

        $version = $stream.ReadByte()
        $method = $stream.ReadByte()
        return ($version -eq 5 -and ($method -eq 0 -or $method -eq 2))
    } catch {
        return $false
    } finally {
        if ($null -ne $stream) {
            try { $stream.Dispose() } catch {}
        }
        if ($null -ne $waitHandle) {
            try { $waitHandle.Close() } catch {}
        }
        try { $client.Close() } catch {}
    }
}

function Test-IsRunning {
    if (Test-Socks5Ready -Port $script:State.Socks5Port) {
        return $true
    }

    try {
        return (@(Get-Process -Name 'msboost' -ErrorAction SilentlyContinue).Count -gt 0)
    } catch {
        return $false
    }
}

function Wait-MSBoostStopped {
    param([int]$TimeoutMs = 2500)

    $watch = [Diagnostics.Stopwatch]::StartNew()
    do {
        if (-not (Test-IsRunning)) {
            return $true
        }
        Start-Sleep -Milliseconds 100
    } while ($watch.ElapsedMilliseconds -lt $TimeoutMs)

    return (-not (Test-IsRunning))
}

function Show-MainMenu {
    Set-ScreenColor -Color White
    Clear-Host
    Write-Header -Title 'MSBOOST启动器'

    Write-Host '  当前配置（最近加载）：' -NoNewline
    if ([string]::IsNullOrWhiteSpace($script:State.ConfigFile) -or
        $script:State.ConfigFile -eq '未加载' -or
        $script:State.ConfigFile -eq '未记录') {
        Write-Host '未加载' -ForegroundColor Red
    } else {
        Write-Host $script:State.ConfigFile -ForegroundColor Green
    }

    Write-Host '  SOCKS5 地址（跟随配置）：' -NoNewline
    Write-Host ('127.0.0.1:{0}' -f $script:State.Socks5Port) -ForegroundColor Green

    Write-Host '  运行状态：' -NoNewline
    $running = Test-IsRunning
    if ($running) {
        Write-Host '● 运行中' -ForegroundColor Green
    } else {
        Write-Host '● 未运行' -ForegroundColor Red
    }

    Write-Host ''
    Write-Host '    [0] 加载配置文件'
    Write-Host '    [1] 启动MSBOOST'
    Write-Host '    [2] 停止MSBOOST'
    Write-Host '    [3] 检查MSBOOST IP 地址'
    Write-Host '    [Q] 退出启动器'
    Write-Host ''
    Write-Host ('  ' + ('-' * 68))
    Write-Host '  【重要】MSBOOST运行期间请勿关闭本窗口，可以将窗口最小化。'
    Write-Host '  切换配置前，请先停止正在运行的MSBOOST。'
    Write-Host ('  ' + ('=' * 68))
    Write-Host ''
}

function Show-MissingProgram {
    Set-ScreenColor -Color Red
    Clear-Host
    Write-Header -Title 'MSBOOST启动器错误' -Color Red
    Write-Host '  [错误] 没有在 BAT 所在目录找到 msboost.exe。'
    Write-Host ''
    Write-Host '  请把以下文件放在同一个文件夹：'
    Write-Host '    一、本启动器脚本（.bat）'
    Write-Host '    二、msboost.exe（MSBOOST主程序）'
    Write-Host '    三、一个或多个 .json 客户端配置文件'
    Write-Host ''
    Write-Host ('  当前检查目录：{0}' -f $script:Root)
    Write-Host ''
    [void](Read-Host '  按回车键退出')
    Restore-ConsoleColors
}

function Select-AndReplaceConfig {
    Set-ScreenColor -Color White
    Clear-Host
    Write-Header -Title '选择配置文件'

    $files = @(Get-ChildItem -LiteralPath $script:Root -Filter '*.json' -ErrorAction SilentlyContinue |
        Where-Object { -not $_.PSIsContainer } |
        Sort-Object Name)

    if ($files.Count -eq 0) {
        Start-ErrorPage -Title '加载配置失败'
        Write-Host '  [错误] 当前目录没有找到 .json 配置文件。'
        Wait-ForMenu
        return
    }

    Write-Host ('  已找到 {0} 个配置文件：' -f $files.Count)
    Write-Host ''
    for ($index = 0; $index -lt $files.Count; $index++) {
        Write-Host ('    [{0}] {1}' -f ($index + 1), $files[$index].Name)
    }
    Write-Host ''

    while ($true) {
        $raw = (Read-Host '  输入序号（输入 0 返回）').Trim()
        $number = 0
        if ([int]::TryParse($raw, [ref]$number) -and $number -ge 0 -and $number -le $files.Count) {
            break
        }
        Write-Host '  [提示] 输入无效，请重新输入。'
    }

    if ($number -eq 0) {
        return
    }

    $selected = $files[$number - 1]
    if (Test-IsRunning) {
        Write-Host ''
        Write-Host '  [提示] 当前MSBOOST正在运行，暂不加载新配置。'
        Write-Host '  请先返回主菜单选择 [2] 停止MSBOOST，再加载配置。'
        Write-Host '  这样可以避免新旧端口或控制接口发生错位。'
        Wait-ForMenu
        return
    }

    Write-Host ''
    Write-Host ('  正在加载：{0}' -f $selected.Name)
    $result = Invoke-Captured -Arguments @('replace', 'config', $selected.FullName)
    Write-Host ''

    if ($result.ExitCode -eq 0) {
        $port = Get-EffectivePort -SelectedConfigPath $selected.FullName -Fallback $script:State.Socks5Port
        $script:State.ConfigFile = $selected.Name
        $script:State.Socks5Port = $port
        $saved = Save-State

        Write-Host '  [成功] 配置已替换。'
        Write-Host ('  SOCKS5 端口已同步为：{0}' -f $port)
        if (-not $saved) {
            Write-Host '  [警告] 配置已替换，但无法永久保存配置名称和端口。'
        }
    } else {
        Show-NativeError -Action '加载配置' -ExitCode $result.ExitCode -Output $result.Output
    }

    Wait-ForMenu
}

function Start-Boost {
    Set-ScreenColor -Color White
    Clear-Host
    Write-Header -Title '启动MSBOOST'

    if (Test-IsRunning) {
        Write-Host '  [提示] MSBOOST已经在运行，无需重复启动。'
        Wait-ForMenu
        return
    }

    [void](Sync-EffectivePort)
    Write-Host '  正在启动，请稍候……'
    Write-Host ''

    $code = Invoke-Silent -Arguments @('start')
    if ($code -eq 0) {
        Write-Host '  [成功] MSBOOST已启动。'
        Write-Host ('  本地 SOCKS5 地址：127.0.0.1:{0}' -f $script:State.Socks5Port)
    } else {
        Start-ErrorPage -Title '启动MSBOOST失败'
        Write-Host '  [失败] MSBOOST启动失败。'
        Write-Host ('  程序返回代码：{0}' -f $code)
        Write-Host '  请确认已经加载有效配置，且本地端口没有被占用。'
    }

    Wait-ForMenu
}

function Stop-Boost {
    Set-ScreenColor -Color White
    Clear-Host
    Write-Header -Title '停止MSBOOST'

    Write-Host '  正在停止，请稍候……'
    Write-Host ''

    $code = Invoke-Silent -Arguments @('stop')
    if ($code -eq 0) {
        if (Wait-MSBoostStopped) {
            Write-Host '  [成功] MSBOOST已停止。'
        } else {
            Start-ErrorPage -Title '停止MSBOOST失败'
            Write-Host '  [失败] 已发送停止命令，但MSBOOST仍在运行。'
            Write-Host '  可能是管理接口异常，请稍后重试。'
        }
    } elseif (-not (Test-IsRunning)) {
        Write-Host '  [提示] MSBOOST当前未运行，无需停止。'
    } else {
        Start-ErrorPage -Title '停止MSBOOST失败'
        Write-Host '  [失败] 停止MSBOOST失败。'
        Write-Host ('  程序返回代码：{0}' -f $code)
    }

    Wait-ForMenu
}

function Invoke-ExitIpLookup {
    param([string]$ProxyAddress)
    if (-not (Test-Path -LiteralPath $script:CurlPath -PathType Leaf)) {
        return [pscustomobject]@{ Success = $false; Address = ''; Error = '没有找到 curl.exe，无法查询出口 IP' }
    }
    $result = @()
    $code = 1
    try {
        $result = @(& $script:CurlPath -q --fail --silent --show-error --connect-timeout 10 --max-time 20 --socks5-hostname $ProxyAddress $script:IpCheckUrl 2>&1 |
            ForEach-Object { [string]$_ })
        if ($null -ne $LASTEXITCODE) {
            $code = [int]$LASTEXITCODE
        }
    } catch {
        $result = @($_.Exception.Message)
    }

    $ipText = [string]::Join('', [string[]]$result).Trim()
    $parsedAddress = $null
    $validIpv4 = [Net.IPAddress]::TryParse($ipText, [ref]$parsedAddress) -and
        $parsedAddress.AddressFamily -eq [Net.Sockets.AddressFamily]::InterNetwork

    if ($code -eq 0 -and $validIpv4) {
        return [pscustomobject]@{ Success = $true; Address = $ipText; Error = '' }
    }
    $reason = '无法通过当前 SOCKS5 端口查询 IP，请确认 MSBOOST 已启动'
    if (Save-ErrorDetails -Action '检查出口 IP' -ExitCode $code -Output $result) {
        $reason += '；详情见 msboost-last-error.log'
    }
    return [pscustomobject]@{ Success = $false; Address = ''; Error = $reason }
}

function Check-IpAddress {
    Set-ScreenColor -Color White
    Clear-Host
    Write-Header -Title '检查MSBOOST IP 地址'

    # Use one saved configuration snapshot and one resolved node address for this check.
    $config = Get-EffectiveConfig
    $targets = Get-LatencyTargets -Config $config
    if (-not $targets.RelayError) {
        $resolvedRelay = Resolve-LatencyRelayAddress -Address $targets.RelayAddress -TimeoutMs $script:LatencyTimeoutMs
        if ($resolvedRelay.Error) { $targets.RelayError = $resolvedRelay.Error }
        else { $targets.RelayAddress = $resolvedRelay.Address }
    }
    $effectivePort = $targets.Socks5Port
    if ($effectivePort -eq 0) {
        $selectedPath = Join-Path $script:Root $script:State.ConfigFile
        $effectivePort = Get-PortFromJsonFile -Path $selectedPath -Fallback $script:State.Socks5Port
    }
    if ($effectivePort -ne $script:State.Socks5Port) {
        $script:State.Socks5Port = $effectivePort
        [void](Save-State)
    }
    $proxyAddress = '127.0.0.1:{0}' -f $effectivePort
    Write-Host ('  SOCKS5 地址：{0}' -f $proxyAddress)
    if (-not $targets.RelayError) {
        Write-Host ('  中转测试目标：{0}:{1}' -f $targets.RelayAddress, $targets.RelayPort)
    }
    Write-Host ('  游戏测试目标：{0}:{1}' -f $script:GameAddress, $script:GamePort)
    Write-Host '  正在查询出口 IPv4，请稍候……'
    Write-Host ''

    $ipResult = Invoke-ExitIpLookup -ProxyAddress $proxyAddress
    if ($ipResult.Success) {
        Write-Host ('  当前出口 IPv4：{0}' -f $ipResult.Address)
    } else {
        Write-Host ('  当前出口 IPv4：查询失败；{0}' -f $ipResult.Error) -ForegroundColor Yellow
    }
    Write-Host '  正在测量 TCP 延迟，请稍候……'

    $relay = $null
    if ($targets.RelayError) {
        Write-Host ('  当前到中转服务器延迟：{0}' -f $targets.RelayError) -ForegroundColor Yellow
    } else {
        $relay = Measure-LatencySamples -Address $targets.RelayAddress -Port $targets.RelayPort `
            -Count $script:LatencySampleCount -TimeoutMs $script:LatencyTimeoutMs
        Write-Host ('  当前到中转服务器延迟：{0}' -f (Format-LatencyResult $relay -Compact))
    }
    if ($targets.GameError) {
        $game = New-UnavailableLatencyResult $targets.GameError
    } else {
        # The full-path result is independent of entrance TCP/DNS success.
        $game = Invoke-FullPathTcpSamples -Socks5Port $effectivePort -GameAddress $script:GameAddress `
            -GamePort $script:GamePort -Count $script:LatencySampleCount -TimeoutMs $script:LatencyTimeoutMs `
            -WarmupTimeoutMs $script:FullPathWarmupTimeoutMs
    }
    Write-Host ('  当前到游戏服务器延迟：{0}' -f (Format-LatencyResult $game -Compact))
    if ($ipResult.Success) {
        Write-Host '  [成功] 已通过当前 SOCKS5 配置完成出口 IP 查询。'
    }

    Wait-ForMenu
}

function Confirm-Exit {
    Set-ScreenColor -Color White
    Clear-Host
    Write-Header -Title '退出启动器'
    if (Test-IsRunning) {
        Write-Host '  [注意] MSBOOST仍在运行，关闭本窗口可能使其停止。'
        Write-Host '  建议返回主菜单，先选择 [2] 停止MSBOOST。'
        Write-Host ''
    }
    $answer = (Read-Host '  确定退出吗？输入 Y 确认，其他键返回').Trim()
    return [string]::Equals($answer, 'Y', [StringComparison]::OrdinalIgnoreCase)
}

if (-not (Test-Path -LiteralPath $script:ExePath -PathType Leaf)) {
    Show-MissingProgram
    exit 1
}

$script:State = Load-State

while ($true) {
    Show-MainMenu
    $choice = (Read-Host '  请选择菜单').Trim().ToUpperInvariant()
    switch ($choice) {
        '0' { Select-AndReplaceConfig }
        '1' { Start-Boost }
        '2' { Stop-Boost }
        '3' { Check-IpAddress }
        'Q' {
            if (Confirm-Exit) {
                Restore-ConsoleColors
                exit 0
            }
        }
        default {
            Write-Host '  [提示] 输入无效，请选择 0、1、2、3 或 Q。'
            Start-Sleep -Milliseconds 900
        }
    }
}
