# Run with Windows PowerShell 5.1. Every socket used by this suite stays on loopback.
param([string]$LauncherPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'MSBOOST启动器.bat'))

$ErrorActionPreference = 'Stop'
$utf8 = New-Object System.Text.UTF8Encoding($false)
$source = [IO.File]::ReadAllText($LauncherPath, $utf8)
$marker = '#<MSBOOST_PS>'
$offset = $source.IndexOf($marker, [StringComparison]::Ordinal)
if ($offset -lt 0) { throw 'Embedded PowerShell marker was not found.' }
$tokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseInput(
    $source.Substring($offset + $marker.Length), [ref]$tokens, [ref]$parseErrors)
if (@($parseErrors).Count -ne 0) { throw ($parseErrors | Out-String) }
# Loading only top-level function definitions never runs the menu or core executable.
$definitions = @($ast.EndBlock.Statements | Where-Object {
    $_ -is [Management.Automation.Language.FunctionDefinitionAst]
} | ForEach-Object { $_.Extent.Text })
. ([ScriptBlock]::Create([string]::Join([Environment]::NewLine, $definitions)))
foreach ($name in @('Invoke-RelayTcpSamples', 'Merge-TcpLatencySamples', 'Measure-LatencySamples')) {
    if (-not (Get-Command $name -CommandType Function -ErrorAction SilentlyContinue)) {
        throw ('Required launcher function is missing: ' + $name)
    }
}

if (-not ('RelayTcpTest.MockServer' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;

namespace RelayTcpTest {
    public sealed class MockServer : IDisposable {
        private readonly TcpListener listener;
        private readonly Thread thread;
        private readonly object gate = new object();
        private readonly List<TcpClient> clients = new List<TcpClient>();
        private readonly List<string> requests = new List<string>();
        private readonly List<string> destinations = new List<string>();
        private readonly List<string> faults = new List<string>();
        private volatile bool stopped;
        private int accepted;
        public readonly int Port;
        public string Mode = "normal";
        public string ResponseTemplate;
        public int GreetingDelayMs;
        public int SocksDelayMs;
        public int ResponseDelayMs;
        public bool FragmentSocks;
        public bool FragmentJson;
        public int Method;
        public int SocksResponseCode;
        public int Accepted { get { return accepted; } }
        public int Active { get { lock (gate) { return clients.Count; } } }
        public string[] Requests { get { lock (gate) { return requests.ToArray(); } } }
        public string[] Destinations { get { lock (gate) { return destinations.ToArray(); } } }
        public string[] Faults { get { lock (gate) { return faults.ToArray(); } } }

        public MockServer() {
            listener = new TcpListener(IPAddress.Loopback, 0);
            listener.Start();
            Port = ((IPEndPoint)listener.LocalEndpoint).Port;
            thread = new Thread(AcceptLoop);
            thread.IsBackground = true;
            thread.Start();
        }
        private void AcceptLoop() {
            while (!stopped) {
                try {
                    TcpClient client = listener.AcceptTcpClient();
                    lock (gate) { clients.Add(client); }
                    Interlocked.Increment(ref accepted);
                    ThreadPool.QueueUserWorkItem(delegate { Handle(client); });
                } catch (SocketException) { return; }
                  catch (ObjectDisposedException) { return; }
            }
        }
        private static byte[] ReadExactly(NetworkStream stream, int count) {
            byte[] bytes = new byte[count];
            int offset = 0;
            while (offset < count) {
                int read = stream.Read(bytes, offset, count - offset);
                if (read == 0) throw new EndOfStreamException();
                offset += read;
            }
            return bytes;
        }
        private static string ReadLine(NetworkStream stream) {
            List<byte> bytes = new List<byte>();
            while (bytes.Count <= 4096) {
                int value = stream.ReadByte();
                if (value < 0) throw new EndOfStreamException();
                if (value == 10) return Encoding.UTF8.GetString(bytes.ToArray()).TrimEnd('\r');
                bytes.Add((byte)value);
            }
            throw new InvalidDataException("Request exceeded 4096 bytes.");
        }
        private void Handle(TcpClient client) {
            try {
                client.NoDelay = true;
                NetworkStream stream = client.GetStream();
                stream.ReadTimeout = 4000;
                stream.WriteTimeout = 4000;
                if (Mode == "direct") return;
                byte[] greeting = ReadExactly(stream, 2);
                byte[] methods = ReadExactly(stream, greeting[1]);
                if (greeting[0] != 5 || Array.IndexOf(methods, (byte)0) < 0)
                    throw new InvalidDataException("Expected SOCKS5 no-auth offer.");
                if (GreetingDelayMs > 0) Thread.Sleep(GreetingDelayMs);
                stream.Write(new byte[] { 5, (byte)Method }, 0, 2);
                if (Method != 0) return;
                byte[] header = ReadExactly(stream, 4);
                if (header[0] != 5 || header[1] != 1 || header[2] != 0)
                    throw new InvalidDataException("Expected SOCKS5 CONNECT.");
                string address;
                if (header[3] == 1) address = new IPAddress(ReadExactly(stream, 4)).ToString();
                else if (header[3] == 4) address = new IPAddress(ReadExactly(stream, 16)).ToString();
                else if (header[3] == 3) address = Encoding.ASCII.GetString(ReadExactly(stream, ReadExactly(stream, 1)[0]));
                else throw new InvalidDataException("Unexpected SOCKS5 address type.");
                byte[] port = ReadExactly(stream, 2);
                lock (gate) { destinations.Add(address + ":" + ((port[0] << 8) | port[1])); }
                if (SocksDelayMs > 0) Thread.Sleep(SocksDelayMs);
                byte[] reply = new byte[] { 5, (byte)SocksResponseCode, 0, 1, 127, 0, 0, 1, 0, 80 };
                if (FragmentSocks) {
                    stream.Write(reply, 0, 4);
                    Thread.Sleep(40);
                    stream.Write(reply, 4, reply.Length - 4);
                } else stream.Write(reply, 0, reply.Length);
                if (SocksResponseCode != 0) return;
                string request = ReadLine(stream);
                lock (gate) { requests.Add(request); }
                Match nonce = Regex.Match(request, "\"nonce\"\\s*:\\s*\"([0-9a-fA-F]{32})\"");
                if (!nonce.Success) throw new InvalidDataException("Request has no 32-hex nonce.");
                if (Mode == "eof") return;
                if (Mode == "stall") { while (!stopped) Thread.Sleep(10); return; }
                if (ResponseDelayMs > 0) Thread.Sleep(ResponseDelayMs);
                string body = ResponseTemplate.Replace("__NONCE__", nonce.Groups[1].Value);
                if (Mode == "echo-target") {
                    Match targetAddress = Regex.Match(request, "\"target_address\"\\s*:\\s*\"([0-9.]+)\"");
                    Match targetPort = Regex.Match(request, "\"target_port\"\\s*:\\s*([0-9]+)");
                    if (!targetAddress.Success || !targetPort.Success)
                        throw new InvalidDataException("Request has no IPv4 target and numeric port.");
                    body = body.Replace("__TARGET_ADDRESS__", targetAddress.Groups[1].Value)
                        .Replace("__TARGET_PORT__", targetPort.Groups[1].Value);
                }
                if (Mode == "oversize") body = new string('x', 4097);
                if (Mode != "no-newline") body += "\n";
                byte[] response = Encoding.UTF8.GetBytes(body);
                if (FragmentJson) {
                    int split = response.Length / 3;
                    stream.Write(response, 0, split);
                    Thread.Sleep(40);
                    stream.Write(response, split, split);
                    Thread.Sleep(40);
                    stream.Write(response, split * 2, response.Length - split * 2);
                } else stream.Write(response, 0, response.Length);
                if (Mode == "no-newline") return;
                // Wait for EOF to detect leaked client sockets after a successful exchange.
                stream.ReadByte();
            } catch (IOException) { }
              catch (SocketException) { }
              catch (ObjectDisposedException) { }
              catch (Exception ex) { lock (gate) { faults.Add(ex.Message); } }
            finally {
                client.Close();
                lock (gate) { clients.Remove(client); }
            }
        }
        public void Dispose() {
            stopped = true;
            listener.Stop();
            TcpClient[] active;
            lock (gate) { active = clients.ToArray(); }
            foreach (TcpClient client in active) client.Close();
            thread.Join(1000);
        }
    }
}
'@
}

$script:Passed = 0
$script:Failed = 0
$script:GoodSamples = '[{"success":true,"milliseconds":60},{"success":true,"milliseconds":60},{"success":true,"milliseconds":60}]'
function New-Response {
    param([string]$Samples = $script:GoodSamples)
    return ('{"version":1,"nonce":"__NONCE__","relay_address":"192.0.2.10","target_address":"51.222.56.192","target_port":8484,"samples":' + $Samples + '}')
}
function New-ErrorResponse {
    param([string]$Code = 'target_not_allowed')
    return ('{"version":1,"nonce":"__NONCE__","relay_address":"192.0.2.10","target_address":"51.222.56.192","target_port":8484,"error":"' + $Code + '"}')
}
function New-Mock {
    $server = New-Object RelayTcpTest.MockServer
    $server.ResponseTemplate = New-Response
    return $server
}
function Invoke-TestProbe {
    param($Server, [int]$TimeoutMs = 3000, [string]$GameAddress = '51.222.56.192', [int]$GamePort = 8484)
    return Invoke-RelayTcpSamples -RelayAddress '192.0.2.10' -ProbePort 20424 `
        -GameAddress $GameAddress -GamePort $GamePort -Socks5Port $Server.Port -TimeoutMs $TimeoutMs
}
function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}
function Assert-Equal {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -ne $Expected) { throw ('{0}: expected <{1}>, got <{2}>' -f $Message, $Expected, $Actual) }
}
function Assert-FailedSamples {
    param($Result)
    Assert-Equal $Result.SuccessCount 0 'Failure success count'
    Assert-Equal $Result.TotalCount 3 'Failure total count'
    Assert-True ($null -eq $Result.AverageMs) 'Failure must never report a zero/fabricated latency.'
    Assert-True (@($Result.Errors).Count -gt 0) 'Failure needs an explanation.'
    Assert-True (-not [string]::IsNullOrWhiteSpace(($Result.Errors -join ''))) 'Failure explanation must be nonempty.'
}
function Assert-RejectedResponse {
    param([string]$Response)
    $server = New-Mock
    try {
        $server.ResponseTemplate = $Response
        $result = Invoke-TestProbe $server
        Assert-FailedSamples $result
        Assert-Equal @($result.Samples).Count 0 'An invalid response must discard every sample'
    } finally { $server.Dispose() }
}
function Wait-Closed {
    param($Server)
    $watch = [Diagnostics.Stopwatch]::StartNew()
    while ($Server.Active -gt 0 -and $watch.ElapsedMilliseconds -lt 1000) { Start-Sleep -Milliseconds 10 }
    Assert-Equal $Server.Active 0 'Client socket was not released'
    Assert-Equal @($Server.Faults).Count 0 'Mock protocol fault'
}
function New-SampleResult {
    param([object[]]$Values)
    $samples = @($Values | ForEach-Object {
        if ($null -eq $_) { [pscustomobject]@{ Success = $false; Milliseconds = $null; Error = 'test failure' } }
        else { [pscustomobject]@{ Success = $true; Milliseconds = [double]$_; Error = '' } }
    })
    $successful = @($samples | Where-Object { $_.Success })
    $average = $null
    if ($successful.Count -gt 0) { $average = ($successful | Measure-Object Milliseconds -Average).Average }
    return [pscustomobject]@{
        SuccessCount = $successful.Count; TotalCount = 3; AverageMs = $average
        Errors = @('test failure'); Samples = $samples
    }
}
function Test-Case {
    param([string]$Name, [scriptblock]$Body)
    try { & $Body; $script:Passed++; Write-Host ('PASS ' + $Name) }
    catch { $script:Failed++; Write-Host ('FAIL {0}: {1}' -f $Name, $_.Exception.Message) -ForegroundColor Red }
}

Test-Case 'request uses SOCKS to reach relay probe and binds its target with a nonce' {
    $server = New-Mock
    try {
        $result = Invoke-TestProbe $server
        Assert-Equal $result.SuccessCount 3 'Success count'
        Assert-Equal $result.TotalCount 3 'Total count'
        Assert-Equal $result.AverageMs 60 'Measured remote TCP latency'
        Assert-Equal @($result.Samples).Count 3 'Returned sample count'
        Assert-Equal @($server.Requests).Count 1 'One batch request'
        Assert-Equal $server.Destinations[0] '192.0.2.10:20424' 'SOCKS destination'
        $request = $server.Requests[0] | ConvertFrom-Json
        Assert-Equal $request.version 1 'Protocol version'
        Assert-True ($request.nonce -cmatch '^[0-9a-fA-F]{32}$') 'Nonce must be 32 hex characters.'
        Assert-Equal $request.target_address '51.222.56.192' 'Request target address'
        Assert-Equal $request.target_port 8484 'Request target port'
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'each invocation creates a fresh response nonce' {
    $server = New-Mock
    try {
        [void](Invoke-TestProbe $server)
        [void](Invoke-TestProbe $server)
        Assert-Equal @($server.Requests).Count 2 'Request count'
        $first = $server.Requests[0] | ConvertFrom-Json
        $second = $server.Requests[1] | ConvertFrom-Json
        Assert-True ($first.nonce -cne $second.nonce) 'Repeated nonce could accept a stale response.'
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'changing the game target uses the requested IPv4 and port without changing the relay service' {
    $server = New-Mock
    try {
        $server.Mode = 'echo-target'
        $server.ResponseTemplate = (New-Response).Replace('51.222.56.192', '__TARGET_ADDRESS__').Replace('8484', '__TARGET_PORT__')
        $original = Invoke-TestProbe $server
        $changed = Invoke-TestProbe $server -GameAddress '35.155.204.207' -GamePort 8585
        Assert-Equal $original.SuccessCount 3 'Original target succeeds'
        Assert-Equal $changed.SuccessCount 3 'Changed target succeeds'
        Assert-Equal $changed.AverageMs 60 'Changed target uses reported TCP samples'
        Assert-Equal @($server.Requests).Count 2 'Each target creates a request'
        $request = $server.Requests[1] | ConvertFrom-Json
        Assert-Equal $request.target_address '35.155.204.207' 'New target address is sent to relay'
        Assert-Equal $request.target_port 8585 'New target port is sent to relay'
        Assert-Equal $server.Destinations[1] '192.0.2.10:20424' 'Relay probe endpoint remains the same'
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'a response for the old target cannot satisfy a changed game target request' {
    $server = New-Mock
    try {
        $result = Invoke-TestProbe $server -GameAddress '35.155.204.207' -GamePort 8585
        Assert-FailedSamples $result
        Assert-Equal @($result.Samples).Count 0 'Old-target samples are discarded'
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'a valid target_not_allowed response produces a useful target settings error' {
    $server = New-Mock
    try {
        $server.ResponseTemplate = New-ErrorResponse
        $result = Invoke-TestProbe $server
        Assert-FailedSamples $result
        Assert-Equal @($result.Samples).Count 0 'Target denial never supplies samples'
        Assert-True (($result.Errors -join ' ') -match [regex]::Escape('中转测速服务不允许该目标，请使用公网 IPv4 和有效端口，或检查服务器目标设置')) 'Target denial must explain how to fix the request or relay setting.'
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'target denial is accepted only after verifying response identity' {
    foreach ($response in @(
        (New-ErrorResponse).Replace('__NONCE__', '00000000000000000000000000000000'),
        (New-ErrorResponse).Replace('192.0.2.10', '192.0.2.8'),
        (New-ErrorResponse).Replace('51.222.56.192', '35.155.204.207'),
        (New-ErrorResponse).Replace('8484', '8585'),
        (New-ErrorResponse).Replace('"version":1', '"version":2')
    )) {
        $server = New-Mock
        try {
            $server.ResponseTemplate = $response
            $result = Invoke-TestProbe $server
            Assert-FailedSamples $result
            Assert-Equal @($result.Samples).Count 0 'Invalid error identity cannot supply samples'
            Assert-True (($result.Errors -join ' ') -notmatch '中转测速服务不允许该目标') 'An identity mismatch must not be trusted as a target denial.'
            Wait-Closed $server
        } finally { $server.Dispose() }
    }
}
Test-Case 'unknown and non-string top-level error codes are rejected' {
    foreach ($response in @(
        (New-ErrorResponse 'unknown_error'),
        (New-ErrorResponse).Replace('"error":"target_not_allowed"', '"error":["target_not_allowed"]'),
        (New-ErrorResponse).Replace('"error":"target_not_allowed"', '"error":null'),
        (New-ErrorResponse).Replace('"error":"target_not_allowed"', '"error":true')
    )) {
        $server = New-Mock
        try {
            $server.ResponseTemplate = $response
            $result = Invoke-TestProbe $server
            Assert-FailedSamples $result
            Assert-Equal @($result.Samples).Count 0 'Unknown or malformed error cannot supply samples'
            Assert-True (($result.Errors -join ' ') -notmatch '中转测速服务不允许该目标') 'Only the exact supported error code can produce a target denial message.'
        } finally { $server.Dispose() }
    }
}
Test-Case 'a top-level error cannot coexist with samples even when the samples look successful' {
    foreach ($samples in @($script:GoodSamples, '[]', 'null')) {
        $response = (New-ErrorResponse).TrimEnd('}') + ',"samples":' + $samples + '}'
        $server = New-Mock
        try {
            $server.ResponseTemplate = $response
            $result = Invoke-TestProbe $server
            Assert-FailedSamples $result
            Assert-Equal @($result.Samples).Count 0 'Error plus samples invalidates the whole response'
            Assert-True (($result.Errors -join ' ') -notmatch '中转测速服务不允许该目标') 'A contradictory response must fail schema validation before processing its error.'
        } finally { $server.Dispose() }
    }
}
Test-Case 'SOCKS and JSON fragments are read completely' {
    $server = New-Mock
    try {
        $server.FragmentSocks = $true
        $server.FragmentJson = $true
        $result = Invoke-TestProbe $server
        Assert-Equal $result.SuccessCount 3 'Fragmented response success'
        Assert-Equal $result.AverageMs 60 'Fragmented sample value'
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'response transport and proxy setup time never inflate remote sample milliseconds' {
    $server = New-Mock
    try {
        $server.GreetingDelayMs = 60
        $server.SocksDelayMs = 60
        $server.ResponseDelayMs = 120
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $result = Invoke-TestProbe $server
        Assert-Equal $result.AverageMs 60 'Only relay-reported TCP duration belongs in the result'
        Assert-True ($watch.ElapsedMilliseconds -ge 220) 'Mock delays were not observed.'
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'one deadline covers SOCKS negotiation and response reading' {
    $server = New-Mock
    try {
        $server.GreetingDelayMs = 180
        $server.SocksDelayMs = 180
        $server.ResponseDelayMs = 180
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $result = Invoke-TestProbe $server -TimeoutMs 420
        Assert-FailedSamples $result
        Assert-True (($result.Errors -join ' ') -match '超时') 'Shared-deadline failure should report a concise timeout reason.'
        Assert-True ($watch.ElapsedMilliseconds -lt 1100) 'Deadline restarted for each stage.'
    } finally { $server.Dispose() }
}
Test-Case 'empty EOF, missing newline, oversized line and stalled service do not yield samples' {
    foreach ($mode in @('eof', 'no-newline', 'oversize', 'stall')) {
        $server = New-Mock
        try {
            $server.Mode = $mode
            $result = Invoke-TestProbe $server -TimeoutMs 350
            Assert-FailedSamples $result
            Assert-Equal @($result.Samples).Count 0 ('Discard samples for ' + $mode)
            if ($mode -eq 'stall') {
                Assert-True (($result.Errors -join ' ') -match '超时') 'Stalled response should report a concise timeout reason.'
            }
        } finally { $server.Dispose() }
    }
}
Test-Case 'proxy authentication and proxy connection failure remain errors' {
    foreach ($mode in @('auth', 'refused')) {
        $server = New-Mock
        try {
            if ($mode -eq 'auth') { $server.Method = 2 } else { $server.SocksResponseCode = 5 }
            Assert-FailedSamples (Invoke-TestProbe $server)
            Assert-Equal @($server.Requests).Count 0 'No probe request may follow a failed SOCKS setup'
            Wait-Closed $server
        } finally { $server.Dispose() }
    }
}
Test-Case 'closed local proxy does not cause a direct connection fallback' {
    $server = New-Mock
    $port = $server.Port
    $server.Dispose()
    $result = Invoke-RelayTcpSamples -RelayAddress '192.0.2.10' -ProbePort 20424 `
        -GameAddress '51.222.56.192' -GamePort 8484 -Socks5Port $port -TimeoutMs 3000
    Assert-FailedSamples $result
    Assert-True (($result.Errors -join ' ') -match '本地代理未启动或端口未监听') 'Closed local SOCKS port should have a readable local-proxy reason.'
}
Test-Case 'wrong nonce rejects every sample' {
    Assert-RejectedResponse ((New-Response).Replace('__NONCE__', '00000000000000000000000000000000'))
}
Test-Case 'wrong relay identity rejects every sample' {
    Assert-RejectedResponse ((New-Response).Replace('192.0.2.10', '192.0.2.8'))
}
Test-Case 'wrong game address or game port rejects every sample' {
    Assert-RejectedResponse ((New-Response).Replace('51.222.56.192', '192.0.2.9'))
    Assert-RejectedResponse ((New-Response).Replace('8484', '8585'))
}
Test-Case 'identity fields must be strings and target port must be a JSON integer' {
    Assert-RejectedResponse ((New-Response).Replace('"nonce":"__NONCE__"', '"nonce":["__NONCE__"]'))
    Assert-RejectedResponse ((New-Response).Replace('"relay_address":"192.0.2.10"', '"relay_address":["192.0.2.10"]'))
    Assert-RejectedResponse ((New-Response).Replace('"target_address":"51.222.56.192"', '"target_address":["51.222.56.192"]'))
    Assert-RejectedResponse ((New-Response).Replace('"target_port":8484', '"target_port":"8484"'))
}
Test-Case 'unknown or nonnumeric protocol version rejects every sample' {
    Assert-RejectedResponse ((New-Response).Replace('"version":1', '"version":2'))
    Assert-RejectedResponse ((New-Response).Replace('"version":1', '"version":"1"'))
}
Test-Case 'missing identity fields and malformed JSON reject every sample' {
    foreach ($field in @('"nonce":"__NONCE__",', '"relay_address":"192.0.2.10",', '"target_address":"51.222.56.192",', '"target_port":8484,')) {
        Assert-RejectedResponse ((New-Response).Replace($field, ''))
    }
    Assert-RejectedResponse '{invalid-json}'
    Assert-RejectedResponse 'null'
    Assert-RejectedResponse '[]'
}
Test-Case 'sample array must contain exactly three entries' {
    Assert-RejectedResponse (New-Response '[]')
    Assert-RejectedResponse (New-Response '[{"success":true,"milliseconds":60}]')
    Assert-RejectedResponse (New-Response ($script:GoodSamples.TrimEnd(']') + ',{"success":true,"milliseconds":60}]'))
}
Test-Case 'success flag must be a JSON boolean' {
    Assert-RejectedResponse ((New-Response).Replace('"success":true', '"success":"true"'))
    Assert-RejectedResponse ((New-Response).Replace('"success":true', '"success":1'))
    Assert-RejectedResponse ((New-Response).Replace('"success":true', '"success":null'))
}
Test-Case 'successful duration must be a finite JSON number within the attempt budget' {
    foreach ($value in @('-1', '2000.1', '"60"', 'null', 'true', 'NaN', '1e309')) {
        Assert-RejectedResponse ((New-Response).Replace('"milliseconds":60', ('"milliseconds":' + $value)))
    }
}
Test-Case 'failed samples require a supported error code' {
    Assert-RejectedResponse (New-Response '[{"success":false,"error":"unknown"},{"success":true,"milliseconds":60},{"success":true,"milliseconds":60}]')
    Assert-RejectedResponse (New-Response '[{"success":false},{"success":true,"milliseconds":60},{"success":true,"milliseconds":60}]')
}
Test-Case 'mixed remote successes and failures preserve original sample positions' {
    $server = New-Mock
    try {
        $server.ResponseTemplate = New-Response '[{"success":true,"milliseconds":50},{"success":false,"error":"refused"},{"success":true,"milliseconds":70}]'
        $result = Invoke-TestProbe $server
        Assert-Equal $result.SuccessCount 2 'Mixed success count'
        Assert-Equal $result.AverageMs 60 'Mean of successful remote samples'
        Assert-Equal @($result.Samples).Count 3 'All positions preserved'
        Assert-True (-not $result.Samples[1].Success) 'Failed second sample moved or disappeared.'
        Assert-True ($null -eq $result.Samples[1].Milliseconds) 'Failed sample acquired a zero duration.'
        Assert-True (-not [string]::IsNullOrWhiteSpace([string]$result.Samples[1].Error)) 'Failed sample lacks an error.'
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'all supported remote failure codes produce explanatory errors without a mean' {
    foreach ($code in @('timeout', 'refused', 'unreachable', 'failed')) {
        $server = New-Mock
        try {
            $entry = '{"success":false,"error":"' + $code + '"}'
            $server.ResponseTemplate = New-Response ('[' + (@($entry, $entry, $entry) -join ',') + ']')
            $result = Invoke-TestProbe $server
            Assert-FailedSamples $result
            Assert-Equal @($result.Samples).Count 3 'Remote failed sample positions'
        } finally { $server.Dispose() }
    }
}
Test-Case 'independent 165 ms and 60 ms segment means produce 225 ms total TCP latency' {
    $relay = New-SampleResult @(165, 165, 165)
    $remote = New-SampleResult @(60, 60, 60)
    $total = Merge-TcpLatencySamples -Relay $relay -Remote $remote
    Assert-Equal $total.SuccessCount 3 'Combined success count'
    Assert-Equal $total.TotalCount 3 'Combined total count'
    Assert-Equal $total.AverageMs 225 'Combined path duration'
    Assert-Equal $total.RelaySuccessCount 3 'Client-to-relay success count'
    Assert-Equal $total.RemoteSuccessCount 3 'Relay-to-game success count'
    Assert-Equal @($total.Samples).Count 0 'Independent segment means must not fabricate combined single samples'
}
Test-Case 'partial failures retain every successful sample in each independent segment mean' {
    $relay = New-SampleResult @(100, $null, 300)
    $remote = New-SampleResult @(10, 20, $null)
    $total = Merge-TcpLatencySamples -Relay $relay -Remote $remote
    Assert-Equal $total.SuccessCount 2 'Smaller available sample count'
    Assert-Equal $total.TotalCount 3 'Combined total count'
    Assert-Equal $total.AverageMs 215 'Client-to-relay mean 200 plus relay-to-game mean 15'
    Assert-Equal $total.RelaySuccessCount 2 'Two client-to-relay successes'
    Assert-Equal $total.RemoteSuccessCount 2 'Two relay-to-game successes'
    Assert-True (@($total.Errors).Count -gt 0) 'Partial failures should remain visible to the caller.'
    Assert-Equal @($total.Samples).Count 0 'No synthetic combined samples'
}
Test-Case 'failed remote samples are excluded only from their own segment mean' {
    $relay = New-SampleResult @(165, 165, 165)
    $remote = New-SampleResult @(60, $null, 80)
    $total = Merge-TcpLatencySamples -Relay $relay -Remote $remote
    Assert-Equal $total.SuccessCount 2 'Smaller available sample count'
    Assert-Equal $total.RelaySuccessCount 3 'Three client-to-relay successes'
    Assert-Equal $total.RemoteSuccessCount 2 'Two relay-to-game successes'
    Assert-Equal $total.AverageMs 235 'Mean 165 plus mean 70'
}
Test-Case 'disjoint successful positions still supply two valid independent segment means' {
    $relay = New-SampleResult @(100, $null, $null)
    $remote = New-SampleResult @($null, 20, 30)
    $total = Merge-TcpLatencySamples -Relay $relay -Remote $remote
    Assert-Equal $total.AverageMs 125 'Independent means 100 plus 25'
    Assert-Equal $total.SuccessCount 1 'Smaller available sample count'
    Assert-Equal $total.RelaySuccessCount 1 'One client-to-relay success'
    Assert-Equal $total.RemoteSuccessCount 2 'Two relay-to-game successes'
    Assert-Equal @($total.Samples).Count 0 'No index-paired single samples were measured'
}
Test-Case 'remote failure never discards a slow but successful client-to-relay sample' {
    $relay = New-SampleResult @(900, 100, 100)
    $remote = New-SampleResult @($null, 70, 70)
    $remote.Samples[0].Error = 'timeout'
    $total = Merge-TcpLatencySamples -Relay $relay -Remote $remote
    $expected = (1100.0 / 3) + 70
    Assert-True ([Math]::Abs($total.AverageMs - $expected) -lt 0.000001) 'Expected 436.6666667 ms from all successful segment samples.'
    Assert-True ($total.AverageMs -ge $relay.AverageMs) 'Positive remote time cannot lower the displayed client-to-relay mean.'
    Assert-Equal $total.RelaySuccessCount 3 'Slow successful sample retained'
    Assert-Equal $total.RemoteSuccessCount 2 'Remote timeout excluded'
    Assert-Equal $total.SuccessCount 2 'Smaller available sample count'
}
Test-Case 'either segment with three failed samples prevents a total duration' {
    $success = New-SampleResult @(165, 165, 165)
    $failure = New-SampleResult @($null, $null, $null)
    $remoteFailed = Merge-TcpLatencySamples -Relay $success -Remote $failure
    Assert-FailedSamples $remoteFailed
    Assert-Equal $remoteFailed.RelaySuccessCount 3 'Successful first segment count'
    Assert-Equal $remoteFailed.RemoteSuccessCount 0 'Failed second segment count'
    Assert-Equal @($remoteFailed.Samples).Count 0 'No summed samples on failure'
    $relayFailed = Merge-TcpLatencySamples -Relay $failure -Remote $success
    Assert-FailedSamples $relayFailed
    Assert-Equal $relayFailed.RelaySuccessCount 0 'Failed first segment count'
    Assert-Equal $relayFailed.RemoteSuccessCount 3 'Successful second segment count'
}
Test-Case 'combination recomputes means and counts from raw samples instead of trusting summaries' {
    $relay = New-SampleResult @(165, 165, 165)
    $remote = New-SampleResult @(60, $null, 80)
    $relay.SuccessCount = 0
    $relay.AverageMs = 999
    $remote.SuccessCount = 3
    $remote.AverageMs = 0
    $total = Merge-TcpLatencySamples -Relay $relay -Remote $remote
    Assert-Equal $total.AverageMs 235 'Means are recomputed from actual successful samples'
    Assert-Equal $total.RelaySuccessCount 3 'First raw sample count'
    Assert-Equal $total.RemoteSuccessCount 2 'Second raw sample count'
}
Test-Case 'invalid raw sample status or duration invalidates its entire segment' {
    foreach ($mode in @('string-success', 'negative-duration', 'missing-duration', 'failure-with-duration', 'missing-error')) {
        $relay = New-SampleResult @(165, 165, 165)
        $remote = New-SampleResult @(60, 60, 60)
        switch ($mode) {
            'string-success' { $remote.Samples[0].Success = 'true' }
            'negative-duration' { $remote.Samples[0].Milliseconds = -1 }
            'missing-duration' { $remote.Samples[0].PSObject.Properties.Remove('Milliseconds') }
            'failure-with-duration' { $remote.Samples[0].Success = $false; $remote.Samples[0].Error = 'timeout' }
            'missing-error' { $remote.Samples[0].Success = $false; $remote.Samples[0].Milliseconds = $null }
        }
        $total = Merge-TcpLatencySamples -Relay $relay -Remote $remote
        Assert-FailedSamples $total
        Assert-Equal $total.RemoteSuccessCount 0 ('Invalid segment must have zero usable samples: ' + $mode)
        Assert-Equal @($total.Samples).Count 0 'No summed samples on invalid data'
    }
}
Test-Case 'unavailable service never falls back to only the client-to-relay measurement' {
    $relay = New-SampleResult @(165, 165, 165)
    $remote = [pscustomobject]@{ SuccessCount = 0; TotalCount = 3; AverageMs = $null; Errors = @('service unavailable'); Samples = @() }
    Assert-FailedSamples (Merge-TcpLatencySamples -Relay $relay -Remote $remote)
    Assert-FailedSamples (Merge-TcpLatencySamples -Relay $remote -Remote $relay)
}
Test-Case 'regular direct measurement preserves all three raw samples for independent means' {
    $server = New-Mock
    try {
        $server.Mode = 'direct'
        $result = Measure-LatencySamples -Address '127.0.0.1' -Port $server.Port -Count 3 -TimeoutMs 1000
        Assert-Equal $result.SuccessCount 3 'Direct success count'
        Assert-Equal @($result.Samples).Count 3 'Direct sample positions'
        foreach ($sample in $result.Samples) { Assert-True $sample.Success 'Unexpected direct failure.' }
        Wait-Closed $server
    } finally { $server.Dispose() }
}
Test-Case 'launcher does not embed an SSH administration dependency or root credential variable' {
    $commands = @($ast.FindAll({ param($node) $node -is [Management.Automation.Language.CommandAst] }, $true) | ForEach-Object { $_.GetCommandName() })
    Assert-Equal @($commands | Where-Object { $_ -match '^(ssh|sshpass|plink|putty|scp)(\.exe)?$' }).Count 0 'SSH is not a launcher runtime dependency'
    Assert-True ($source -notmatch '(?im)^\s*\$[^\r\n=]*(ssh.?password|root.?password)\s*=') 'Launcher contains an SSH/root password variable.'
    Assert-True ($source -notmatch '(?i)root@192.0.2.10') 'Launcher contains an administration login target.'
}

Write-Host ('Relay TCP tests: {0} passed, {1} failed.' -f $script:Passed, $script:Failed)
if ($script:Failed -gt 0) { exit 1 }
