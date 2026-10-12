# Run with Windows PowerShell 5.1. All sockets stay on loopback; no real node is used.
param([string]$LauncherPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'MSBOOST启动器_全程TCP测速.bat'))

$ErrorActionPreference = 'Stop'
$script:Utf8 = New-Object Text.UTF8Encoding($false)
$source = [IO.File]::ReadAllText($LauncherPath, $script:Utf8)
$marker = '#<MSBOOST_PS>'
$offset = $source.IndexOf($marker, [StringComparison]::Ordinal)
if ($offset -lt 0) { throw 'Embedded PowerShell marker is missing.' }
$tokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseInput($source.Substring($offset + $marker.Length), [ref]$tokens, [ref]$parseErrors)
if (@($parseErrors).Count -ne 0) { throw ($parseErrors | Out-String) }
$definitions = @($ast.EndBlock.Statements | Where-Object { $_ -is [Management.Automation.Language.FunctionDefinitionAst] } | ForEach-Object { $_.Extent.Text })
. ([ScriptBlock]::Create([string]::Join([Environment]::NewLine, $definitions)))
$script:Root = Split-Path -Parent $LauncherPath
$script:ExePath = Join-Path $script:Root 'MSBOOST.exe'
$script:FullPathProbePort = 20424
$script:FullPathWarmupTimeoutMs = 5000
$script:GameAddress = '51.222.56.192'
$script:GamePort = 8484
$script:LatencySampleCount = 3
$script:LatencyTimeoutMs = 2000
$script:DefaultPort = 1080
$script:State = [pscustomobject]@{ ConfigFile = 'unused.json'; Socks5Port = 10086 }
if (-not (Get-Command Invoke-FullPathTcpSamples -CommandType Function -ErrorAction SilentlyContinue)) {
    throw 'Invoke-FullPathTcpSamples is missing.'
}

if (-not ('FullPathLatencyTest.MockServer' -as [type])) {
    Add-Type -ReferencedAssemblies System.Web.Extensions -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Web.Script.Serialization;

namespace FullPathLatencyTest {
    public sealed class RequestRecord {
        public int Session;
        public string Operation;
        public string Nonce;
        public string TargetAddress;
        public int TargetPort;
    }

    public sealed class MockServer : IDisposable {
        private readonly TcpListener listener;
        private readonly Thread acceptThread;
        private readonly object gate = new object();
        private readonly List<TcpClient> clients = new List<TcpClient>();
        private readonly List<RequestRecord> records = new List<RequestRecord>();
        private readonly List<string> destinations = new List<string>();
        private readonly List<string> faults = new List<string>();
        private volatile bool stopped;
        private int accepted;
        private int hellos;
        private int connects;
        public readonly int Port;
        public int Method;
        public int SocksReplyCode;
        public int GreetingDelayMs;
        public int SocksDelayMs;
        public int HelloDelayMs;
        public int ConnectDelayMs = 70;
        public int FragmentSize;
        public int FragmentDelayMs;
        public int[] ConnectDelays;
        public string HelloMode = "ok";
        public string[] HelloModes;
        public string[] ConnectModes;
        public int Accepted { get { return accepted; } }
        public int HelloCount { get { return hellos; } }
        public int ConnectCount { get { return connects; } }
        public int Active { get { lock (gate) { return clients.Count; } } }
        public RequestRecord[] Records { get { lock (gate) { return records.ToArray(); } } }
        public string[] Destinations { get { lock (gate) { return destinations.ToArray(); } } }
        public string[] Faults { get { lock (gate) { return faults.ToArray(); } } }

        public MockServer() {
            listener = new TcpListener(IPAddress.Loopback, 0);
            listener.Start();
            Port = ((IPEndPoint)listener.LocalEndpoint).Port;
            acceptThread = new Thread(AcceptLoop);
            acceptThread.IsBackground = true;
            acceptThread.Start();
        }
        private void AcceptLoop() {
            while (!stopped) {
                try {
                    TcpClient client = listener.AcceptTcpClient();
                    lock (gate) { clients.Add(client); }
                    int session = Interlocked.Increment(ref accepted) - 1;
                    ThreadPool.QueueUserWorkItem(delegate { Handle(client, session); });
                } catch (SocketException) { if (stopped) return; throw; }
                  catch (ObjectDisposedException) { return; }
            }
        }
        private static byte[] ReadExactly(NetworkStream stream, int count) {
            byte[] buffer = new byte[count];
            int offset = 0;
            while (offset < count) {
                int read = stream.Read(buffer, offset, count - offset);
                if (read == 0) throw new EndOfStreamException();
                offset += read;
            }
            return buffer;
        }
        private static string ReadLine(NetworkStream stream) {
            List<byte> bytes = new List<byte>();
            while (bytes.Count <= 4096) {
                int next = stream.ReadByte();
                if (next < 0) throw new EndOfStreamException();
                if (next == 10) return new UTF8Encoding(false, true).GetString(bytes.ToArray());
                bytes.Add((byte)next);
            }
            throw new InvalidDataException("The client request exceeds its frame limit.");
        }
        private void Write(NetworkStream stream, byte[] bytes) {
            if (FragmentSize <= 0) { stream.Write(bytes, 0, bytes.Length); return; }
            for (int offset = 0; offset < bytes.Length; offset += FragmentSize) {
                int count = Math.Min(FragmentSize, bytes.Length - offset);
                stream.Write(bytes, offset, count);
                if (FragmentDelayMs > 0 && offset + count < bytes.Length) Thread.Sleep(FragmentDelayMs);
            }
        }
        private bool Respond(NetworkStream stream, Dictionary<string, object> request, string node, string mode) {
            if (mode == "eof") return false;
            if (mode == "invalid-utf8") { Write(stream, new byte[] { 0xc3, 0x28, 0x0a }); return true; }
            if (mode == "oversized") { Write(stream, Encoding.ASCII.GetBytes(new string(' ', 4097) + "\n")); return true; }
            Dictionary<string, object> reply = new Dictionary<string, object>(request);
            reply["node_id"] = node;
            reply["success"] = true;
            if (mode.StartsWith("error:")) { reply["success"] = false; reply["error"] = mode.Substring(6); }
            else if (mode == "bad-node") reply["node_id"] = new string('f', 32);
            else if (mode == "bad-node-format") reply["node_id"] = "not-a-node";
            else if (mode == "bad-nonce") reply["nonce"] = new string('f', 32);
            else if (mode == "bad-target") reply["target_address"] = "51.222.56.193";
            else if (mode == "bad-target-port") reply["target_port"] = 8485;
            else if (mode == "bad-operation") reply["operation"] = "other";
            else if (mode == "bad-version") reply["version"] = 1;
            else if (mode == "version-type") reply["version"] = "2";
            else if (mode == "boolean-type") reply["success"] = "true";
            else if (mode == "port-type") reply["target_port"] = "8484";
            else if (mode == "unknown-field") reply["milliseconds"] = 1.0;
            else if (mode == "nested") reply["success"] = new Dictionary<string, object> { { "value", true } };
            else if (mode == "error-with-success") reply["error"] = "timeout";
            else if (mode == "missing-node") reply.Remove("node_id");
            else if (mode == "missing-target") reply.Remove("target_address");
            else if (mode == "missing-error") reply["success"] = false;
            JavaScriptSerializer serializer = new JavaScriptSerializer();
            string json = serializer.Serialize(reply);
            if (mode == "duplicate") json = json.Insert(1, "\"version\":2,");
            if (mode == "escaped") {
                string nonce = (string)request["nonce"];
                json = json.Replace("\"nonce\":\"" + nonce + "\"", "\"nonce\":\"\\u" + ((int)nonce[0]).ToString("x4") + nonce.Substring(1) + "\"");
            }
            byte[] response = Encoding.UTF8.GetBytes(json + "\n");
            if (mode == "bom") {
                byte[] withBom = new byte[response.Length + 3];
                withBom[0] = 0xef; withBom[1] = 0xbb; withBom[2] = 0xbf;
                Array.Copy(response, 0, withBom, 3, response.Length);
                response = withBom;
            }
            if (mode == "truncate") {
                stream.Write(response, 0, response.Length / 2);
                return false;
            }
            Write(stream, response);
            return true;
        }
        private void Handle(TcpClient client, int session) {
            try {
                client.NoDelay = true;
                NetworkStream stream = client.GetStream();
                stream.ReadTimeout = 5000;
                stream.WriteTimeout = 5000;
                byte[] greeting = ReadExactly(stream, 2);
                byte[] methods = ReadExactly(stream, greeting[1]);
                if (greeting[0] != 5 || Array.IndexOf(methods, (byte)0) < 0)
                    throw new InvalidDataException("Expected the SOCKS5 no-auth greeting.");
                if (GreetingDelayMs > 0) Thread.Sleep(GreetingDelayMs);
                stream.Write(new byte[] { 5, (byte)Method }, 0, 2);
                if (Method != 0) return;
                byte[] header = ReadExactly(stream, 4);
                if (header[0] != 5 || header[1] != 1 || header[2] != 0)
                    throw new InvalidDataException("Expected a SOCKS CONNECT request.");
                string address;
                if (header[3] == 1) address = new IPAddress(ReadExactly(stream, 4)).ToString();
                else if (header[3] == 3) address = Encoding.ASCII.GetString(ReadExactly(stream, ReadExactly(stream, 1)[0]));
                else if (header[3] == 4) address = new IPAddress(ReadExactly(stream, 16)).ToString();
                else throw new InvalidDataException("Unknown SOCKS address type.");
                byte[] port = ReadExactly(stream, 2);
                lock (gate) { destinations.Add(address + ":" + ((port[0] << 8) | port[1])); }
                if (SocksDelayMs > 0) Thread.Sleep(SocksDelayMs);
                stream.Write(new byte[] { 5, (byte)SocksReplyCode, 0, 1, 127, 0, 0, 1, 0, 0 }, 0, 10);
                if (SocksReplyCode != 0) return;
                string node = (session + 1).ToString("x32");
                bool greeted = false;
                while (!stopped) {
                    string text = ReadLine(stream);
                    if (text.IndexOf('\\') >= 0 || text[0] == '\ufeff')
                        throw new InvalidDataException("Requests must use unescaped ASCII fields and no BOM.");
                    JavaScriptSerializer serializer = new JavaScriptSerializer();
                    Dictionary<string, object> request = serializer.DeserializeObject(text) as Dictionary<string, object>;
                    if (request == null || !(request["version"] is int) || (int)request["version"] != 2)
                        throw new InvalidDataException("Expected protocol version 2.");
                    string operation = request["operation"] as string;
                    string nonce = request["nonce"] as string;
                    if (nonce == null || !Regex.IsMatch(nonce, "^[0-9a-f]{32}$"))
                        throw new InvalidDataException("Each request needs a lowercase 32-hex nonce.");
                    if (operation != "hello" && operation != "connect")
                        throw new InvalidDataException("Unknown request operation.");
                    if (request.Count != (operation == "hello" ? 3 : 5))
                        throw new InvalidDataException("Unexpected request fields.");
                    RequestRecord record = new RequestRecord();
                    record.Session = session; record.Operation = operation; record.Nonce = nonce;
                    if (operation == "connect") {
                        record.TargetAddress = request["target_address"] as string;
                        if (!(request["target_port"] is int)) throw new InvalidDataException("Target port must be an integer.");
                        record.TargetPort = (int)request["target_port"];
                    }
                    lock (gate) { records.Add(record); }
                    if (operation == "hello") {
                        if (greeted) throw new InvalidDataException("A healthy session must not repeat its hello.");
                        greeted = true;
                        Interlocked.Increment(ref hellos);
                        if (HelloDelayMs > 0) Thread.Sleep(HelloDelayMs);
                        string mode = HelloModes != null && session < HelloModes.Length ? HelloModes[session] : HelloMode;
                        if (!Respond(stream, request, node, mode)) return;
                    } else {
                        if (!greeted) throw new InvalidDataException("Connect requires a successful session hello first.");
                        int index = Interlocked.Increment(ref connects) - 1;
                        int delay = ConnectDelays != null && index < ConnectDelays.Length ? ConnectDelays[index] : ConnectDelayMs;
                        if (delay > 0) Thread.Sleep(delay);
                        string mode = ConnectModes != null && index < ConnectModes.Length ? ConnectModes[index] : "ok";
                        if (!Respond(stream, request, node, mode)) return;
                    }
                }
            } catch (IOException) { }
              catch (ObjectDisposedException) { }
              catch (SocketException) { }
              catch (Exception error) { lock (gate) { faults.Add(error.Message); } }
            finally {
                client.Close();
                lock (gate) { clients.Remove(client); }
            }
        }
        public void Dispose() {
            stopped = true;
            listener.Stop();
            lock (gate) { foreach (TcpClient client in clients) client.Close(); }
            acceptThread.Join(1000);
        }
    }
}
'@
}

$script:Passed = 0
$script:Failed = 0
function Assert-True { param([bool]$Condition, [string]$Message) if (-not $Condition) { throw $Message } }
function Assert-Equal { param($Actual, $Expected, [string]$Message) if ($Actual -ne $Expected) { throw ('{0}: expected <{1}>, got <{2}>' -f $Message, $Expected, $Actual) } }
function Test-Case {
    param([string]$Name, [scriptblock]$Body)
    try { & $Body; $script:Passed++; Write-Host ('PASS ' + $Name) }
    catch { $script:Failed++; Write-Host ('FAIL {0}: {1}' -f $Name, $_.Exception.Message) -ForegroundColor Red }
}
function Invoke-TestProbe {
    param($Server, [int]$TimeoutMs = 1200, [int]$WarmupTimeoutMs = 2000, [string]$GameAddress = '51.222.56.192', [int]$GamePort = 8484)
    return (Invoke-FullPathTcpSamples -Socks5Port $Server.Port -GameAddress $GameAddress -GamePort $GamePort -Count 3 -TimeoutMs $TimeoutMs -WarmupTimeoutMs $WarmupTimeoutMs)
}
function Assert-CleanServer {
    param($Server)
    $deadline = [DateTime]::UtcNow.AddSeconds(2)
    while ($Server.Active -gt 0 -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 10 }
    Assert-Equal $Server.Active 0 'Every session socket must close after measurement.'
    Assert-Equal $Server.Faults.Count 0 ('Mock protocol faults: ' + ($Server.Faults -join '; '))
}
function Assert-FailedSamples {
    param($Result)
    Assert-Equal $Result.SuccessCount 0 'An invalid or failed probe must not produce successes.'
    Assert-True ($null -eq $Result.AverageMs) 'A failed probe must not manufacture a numeric latency.'
    Assert-True (@($Result.Errors).Count -gt 0) 'A failed probe needs an error reason.'
}

Test-Case 'one warm session handles three connect probes with fresh nonces and explicit target echoes' {
    $server = New-Object FullPathLatencyTest.MockServer
    try {
        $result = Invoke-TestProbe $server
        Assert-Equal $result.SuccessCount 3 'Successful sample count'
        Assert-Equal $result.TotalCount 3 'Attempt count'
        Assert-Equal $result.Samples.Count 3 'Individual samples must remain available.'
        Assert-Equal $server.Accepted 1 'A healthy sequence should retain one proxy connection.'
        Assert-Equal $server.HelloCount 1 'Warm up exactly once per session.'
        Assert-Equal $server.ConnectCount 3 'Exactly three connect requests'
        Assert-Equal ($server.Destinations -join ',') '127.0.0.1:20424' 'The SOCKS destination must be the remote local probe service, independent of an ingress IP.'
        Assert-Equal @($server.Records.Nonce | Select-Object -Unique).Count 4 'Hello and all connect requests need unique nonces.'
        foreach ($record in @($server.Records | Where-Object { $_.Operation -eq 'connect' })) {
            Assert-Equal $record.TargetAddress '51.222.56.192' 'The requested game address must be echoed and checked.'
            Assert-Equal $record.TargetPort 8484 'The requested game port must be echoed and checked.'
        }
        Assert-True ($result.AverageMs -ge 55 -and $result.AverageMs -lt 350) 'Result must contain the client-observed connect response time.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'SOCKS and hello warmup latency is excluded but early SOCKS success cannot complete a sample' {
    $server = New-Object FullPathLatencyTest.MockServer
    $server.GreetingDelayMs = 120
    $server.SocksDelayMs = 120
    $server.HelloDelayMs = 350
    $server.ConnectDelayMs = 100
    try {
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $result = Invoke-TestProbe $server -WarmupTimeoutMs 2000
        $watch.Stop()
        Assert-Equal $result.SuccessCount 3 'Delayed warmup must not prevent ordinary samples.'
        Assert-True ($watch.ElapsedMilliseconds -ge 850) 'The warmup and server response delays must actually have elapsed.'
        Assert-True ($result.AverageMs -ge 85 -and $result.AverageMs -lt 300) 'Only measured connect replies belong in the average, not warmup.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'different full-channel response delays change latency without any ingress addition' {
    $fast = New-Object FullPathLatencyTest.MockServer
    $slow = New-Object FullPathLatencyTest.MockServer
    $fast.ConnectDelayMs = 60
    $slow.ConnectDelayMs = 320
    try {
        $a = Invoke-TestProbe $fast
        $b = Invoke-TestProbe $slow
        Assert-Equal $a.SuccessCount 3 'Fast channel successes'
        Assert-Equal $b.SuccessCount 3 'Slow channel successes'
        Assert-True ($a.AverageMs -ge 45 -and $a.AverageMs -lt 220) 'Fast channel must not include an extra relay TCP estimate.'
        Assert-True ($b.AverageMs -ge 300 -and $b.AverageMs -lt 550) 'The measured channel delay must remain in the result.'
        Assert-True (($b.AverageMs - $a.AverageMs) -ge 180) 'Changing the complete channel delay must materially change reported latency.'
        Assert-CleanServer $fast
        Assert-CleanServer $slow
    } finally { $fast.Dispose(); $slow.Dispose() }
}
Test-Case 'fragmented responses count through the final newline' {
    $server = New-Object FullPathLatencyTest.MockServer
    $server.ConnectDelayMs = 40
    $server.FragmentSize = 25
    $server.FragmentDelayMs = 15
    try {
        $result = Invoke-TestProbe $server
        Assert-Equal $result.SuccessCount 3 'Split hello and connect responses must be reconstructed.'
        Assert-True ($result.AverageMs -ge 110) 'Do not stop timing at the first response fragment.'
        Assert-Equal $server.Accepted 1 'Fragmentation does not invalidate a healthy session.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'request serialization and reply parsing delays are excluded from reported network time' {
    $script:UnwrappedFullPathParser = (Get-Command ConvertFrom-FullPathJson -CommandType Function).ScriptBlock
    function ConvertFrom-FullPathJson {
        param([byte[]]$Bytes)
        Start-Sleep -Milliseconds 140
        return (& $script:UnwrappedFullPathParser -Bytes $Bytes)
    }
    function ConvertTo-Json {
        [CmdletBinding()]
        param([Parameter(ValueFromPipeline = $true)]$InputObject, [switch]$Compress)
        process {
            Start-Sleep -Milliseconds 140
            Microsoft.PowerShell.Utility\ConvertTo-Json -InputObject $InputObject -Compress:$Compress
        }
    }
    $server = New-Object FullPathLatencyTest.MockServer
    $server.ConnectDelayMs = 60
    try {
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $result = Invoke-TestProbe $server -WarmupTimeoutMs 2000
        $watch.Stop()
        Assert-Equal $result.SuccessCount 3 'Artificial local JSON processing delays should not invalidate good replies.'
        Assert-True ($watch.ElapsedMilliseconds -ge 1200) 'The deliberately slow serialization and parsing must have run.'
        Assert-True ($result.AverageMs -ge 45 -and $result.AverageMs -lt 180) 'Neither serialization nor validation belongs in the measured network round trip.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'each valid remote failure keeps the session and preserves subsequent successful samples' {
    foreach ($code in @('timeout', 'refused', 'unreachable', 'target_not_allowed', 'failed')) {
        $server = New-Object FullPathLatencyTest.MockServer
        $server.ConnectModes = [string[]]@(('error:' + $code), 'ok', 'ok')
        try {
            $result = Invoke-TestProbe $server
            Assert-Equal $result.SuccessCount 2 ($code + ' must not be a successful latency.')
            Assert-Equal $result.TotalCount 3 'All three attempts should be represented.'
            Assert-True (-not $result.Samples[0].Success -and $null -eq $result.Samples[0].Milliseconds) 'A protocol failure must not manufacture timing.'
            Assert-True (-not [string]::IsNullOrEmpty($result.Samples[0].Error)) 'Map the remote error to a visible reason.'
            Assert-Equal $server.Accepted 1 'A valid remote error must not force reconnection.'
            Assert-Equal $server.HelloCount 1 'A valid remote error must not repeat warmup.'
            Assert-Equal $server.ConnectCount 3 'Continue after a valid negative reply.'
            Assert-CleanServer $server
        } finally { $server.Dispose() }
    }
}
Test-Case 'all valid remote failures have no average and keep a single session' {
    $server = New-Object FullPathLatencyTest.MockServer
    $server.ConnectModes = [string[]]@('error:timeout', 'error:refused', 'error:target_not_allowed')
    try {
        $result = Invoke-TestProbe $server
        Assert-FailedSamples $result
        Assert-Equal $result.TotalCount 3 'Three failed attempts remain visible.'
        Assert-Equal $server.Accepted 1 'Valid negative responses do not corrupt the channel.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'invalid connect replies discard the session and require a fresh hello before remaining attempts' {
    foreach ($mode in @('bad-node', 'bad-node-format', 'bad-nonce', 'bad-target', 'bad-target-port', 'bad-operation',
            'bad-version', 'version-type', 'boolean-type', 'port-type', 'duplicate', 'unknown-field', 'nested',
            'error-with-success', 'missing-node', 'missing-target', 'missing-error', 'error:unknown',
            'escaped', 'bom', 'oversized', 'invalid-utf8', 'eof', 'truncate')) {
        $server = New-Object FullPathLatencyTest.MockServer
        $server.ConnectDelayMs = 5
        $server.ConnectModes = [string[]]@($mode, 'ok', 'ok')
        try {
            $result = Invoke-TestProbe $server
            Assert-Equal $result.SuccessCount 2 ($mode + ' must fail exactly the corrupted attempt.')
            Assert-Equal $result.TotalCount 3 ($mode + ' attempt count')
            Assert-True (-not $result.Samples[0].Success) ($mode + ' must not count as a successful response.')
            Assert-Equal $server.Accepted 2 ($mode + ' must close the bad connection and replace it once.')
            Assert-Equal $server.HelloCount 2 ($mode + ' needs a new authenticated session identity.')
            Assert-Equal $server.ConnectCount 3 ($mode + ' must not consume extra connect attempts.')
            Assert-CleanServer $server
        } finally { $server.Dispose() }
    }
}
Test-Case 'initial hello failure stops immediately without trying the remaining connect requests' {
    foreach ($mode in @('bad-node-format', 'bad-nonce', 'bad-version', 'duplicate', 'unknown-field', 'nested', 'eof', 'invalid-utf8')) {
        $server = New-Object FullPathLatencyTest.MockServer
        $server.HelloMode = $mode
        try {
            $result = Invoke-TestProbe $server
            Assert-FailedSamples $result
            Assert-Equal $server.Accepted 1 ($mode + ' hello failure must not trigger repeated login attempts.')
            Assert-Equal $server.HelloCount 1 'Only one initial hello'
            Assert-Equal $server.ConnectCount 0 'Never send connect before valid warmup.'
            Assert-CleanServer $server
        } finally { $server.Dispose() }
    }
}
Test-Case 'failed reconnect hello preserves prior successful samples and stops remaining work' {
    $server = New-Object FullPathLatencyTest.MockServer
    $server.ConnectModes = [string[]]@('ok', 'bad-nonce', 'ok')
    $server.HelloModes = [string[]]@('ok', 'bad-node-format')
    try {
        $result = Invoke-TestProbe $server
        Assert-Equal $result.SuccessCount 1 'The first complete successful sample must survive a later broken warmup.'
        Assert-True ($result.AverageMs -ge 55) 'Retain the valid sample average.'
        Assert-Equal $server.Accepted 2 'The broken session should receive one replacement attempt.'
        Assert-Equal $server.HelloCount 2 'A replacement hello is required.'
        Assert-Equal $server.ConnectCount 2 'Do not run the third connect after failed replacement hello.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'late replies cannot satisfy the next request because timed-out sessions are closed' {
    $server = New-Object FullPathLatencyTest.MockServer
    $server.ConnectDelays = [int[]]@(380, 40, 40)
    try {
        $result = Invoke-TestProbe $server -TimeoutMs 180
        Assert-Equal $result.SuccessCount 2 'The late first response must remain a timeout.'
        Assert-True (-not $result.Samples[0].Success) 'The first attempt timed out.'
        Assert-True ($result.Samples[1].Success -and $result.Samples[2].Success) 'A fresh session should service the remaining requests.'
        Assert-Equal $server.Accepted 2 'A timed-out connection must be replaced.'
        Assert-Equal $server.HelloCount 2 'Warm up the replacement channel.'
        Assert-True ($result.AverageMs -ge 25 -and $result.AverageMs -lt 180) 'Late data must not contaminate the next sample.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'connect deadline covers the complete fragmented response rather than resetting on each read' {
    $server = New-Object FullPathLatencyTest.MockServer
    $server.ConnectDelayMs = 10
    $server.FragmentSize = 60
    $server.FragmentDelayMs = 75
    try {
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $result = Invoke-TestProbe $server -TimeoutMs 160 -WarmupTimeoutMs 1200
        $watch.Stop()
        Assert-FailedSamples $result
        Assert-Equal $server.ConnectCount 3 'Each failed connect should consume exactly one attempt.'
        Assert-Equal $server.Accepted 3 'Every partial timed-out reply needs its own replacement session.'
        Assert-True ($watch.ElapsedMilliseconds -lt 2200) 'Fragmentation must not turn bounded attempts into unbounded reads.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'SOCKS greeting connect and hello share one warmup deadline' {
    $server = New-Object FullPathLatencyTest.MockServer
    $server.GreetingDelayMs = 110
    $server.SocksDelayMs = 110
    $server.HelloDelayMs = 180
    try {
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $result = Invoke-TestProbe $server -WarmupTimeoutMs 280
        $watch.Stop()
        Assert-FailedSamples $result
        Assert-Equal $server.Accepted 1 'Warmup failure should stop immediately.'
        Assert-Equal $server.ConnectCount 0 'No measured connect may follow incomplete warmup.'
        Assert-True ($watch.ElapsedMilliseconds -lt 600) 'Do not restart the warmup deadline for each phase.'
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}
Test-Case 'unavailable proxy and SOCKS authentication requirements fail without a game timing' {
    $reservation = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $reservation.Start()
    $port = ([Net.IPEndPoint]$reservation.LocalEndpoint).Port
    $reservation.Stop()
    $absent = [pscustomobject]@{ Port = $port }
    Assert-FailedSamples (Invoke-TestProbe $absent -WarmupTimeoutMs 400)
    foreach ($method in @(2, 255)) {
        $server = New-Object FullPathLatencyTest.MockServer
        $server.Method = $method
        try {
            $result = Invoke-TestProbe $server
            Assert-FailedSamples $result
            Assert-True (($result.Errors -join ' ') -match '认证|无认证') 'Authentication failure should remain explicit.'
            Assert-Equal $server.Accepted 1 'Unsupported authentication must not retry repeatedly.'
            Assert-Equal $server.HelloCount 0 'Authentication failure prevents protocol hello.'
            Assert-CleanServer $server
        } finally { $server.Dispose() }
    }
}
Test-Case 'target validation rejects noncanonical IPv4 and bad ports before contacting the proxy' {
    $server = New-Object FullPathLatencyTest.MockServer
    try {
        foreach ($address in @('example.com', '::1', '127.1', '051.222.56.192', '51.222.56.192 ', '')) {
            Assert-FailedSamples (Invoke-TestProbe $server -GameAddress $address)
        }
        foreach ($port in @(0, -1, 65536)) { Assert-FailedSamples (Invoke-TestProbe $server -GamePort $port) }
        Assert-Equal $server.Accepted 0 'Invalid targets must not initiate any proxy connection.'
    } finally { $server.Dispose() }
}
Test-Case 'private IPv4 targets reach policy validation on the node rather than being silently rewritten' {
    $server = New-Object FullPathLatencyTest.MockServer
    $server.ConnectModes = [string[]]@('error:target_not_allowed', 'error:target_not_allowed', 'error:target_not_allowed')
    try {
        $result = Invoke-TestProbe $server -GameAddress '10.0.0.1'
        Assert-FailedSamples $result
        Assert-Equal $server.ConnectCount 3 'The node, not a client substitution, decides target policy.'
        foreach ($record in @($server.Records | Where-Object { $_.Operation -eq 'connect' })) {
            Assert-Equal $record.TargetAddress '10.0.0.1' 'Never replace a denied target with a fixed public game address.'
        }
        Assert-CleanServer $server
    } finally { $server.Dispose() }
}

function Invoke-TestFullPathPage {
    param([string]$Mode = 'ready')
    $script:PageMode = $Mode
    $script:PageLines = New-Object 'Collections.Generic.List[string]'
    $script:PageCalls = New-Object 'Collections.Generic.List[string]'
    $script:PageIpCount = 0
    $script:PageWaitCount = 0
    $script:PageFullCount = 0
    $script:PageExpectedSocks = if ($Mode -in @('missing-config', 'invalid-port')) { 10087 } elseif ($Mode -eq 'second-node') { 10088 } else { 10086 }
    $script:PageExpectedRelay = if ($Mode -eq 'second-node') { '192.0.2.20' } else { '192.0.2.10' }
    $script:PageExpectedRelayPort = if ($Mode -eq 'second-node') { 34069 } else { 20423 }
    $script:State = [pscustomobject]@{ ConfigFile = 'unused.json'; Socks5Port = 1080 }
    function Set-ScreenColor { param($Color) }
    function Clear-Host {}
    function Write-Header { param($Title) }
    function Write-Host { param($Object, $ForegroundColor) [void]$script:PageLines.Add([string]$Object) }
    function Wait-ForMenu { $script:PageWaitCount++ }
    function Save-State { return $true }
    function Get-EffectiveConfig {
        if ($script:PageMode -eq 'missing-config') { return $null }
        $binding = [pscustomobject]@{ port = $script:PageExpectedRelayPort; protocol = 'TCP' }
        $server = [pscustomobject]@{ ipAddress = $script:PageExpectedRelay; portBindings = @($binding) }
        $profile = [pscustomobject]@{ profileName = 'selected'; handshakeMode = 'HANDSHAKE_NO_WAIT'; servers = @($server) }
        $config = [pscustomobject]@{ activeProfile = 'selected'; socks5Port = $script:PageExpectedSocks; profiles = @($profile) }
        if ($script:PageMode -eq 'udp') { $binding.protocol = 'UDP' }
        if ($script:PageMode -eq 'multiple-nodes') {
            $profile.servers += [pscustomobject]@{ ipAddress = '192.0.2.20'; portBindings = @([pscustomobject]@{ port = 34069; protocol = 'TCP' }) }
        }
        if ($script:PageMode -eq 'dns-failure') { $server | Add-Member NoteProperty domainName 'relay.example.invalid' }
        if ($script:PageMode -eq 'invalid-port') { $config.socks5Port = 0 }
        if ($script:PageMode -eq 'authentication') {
            $config | Add-Member NoteProperty socks5Authentication @([pscustomobject]@{ user = 'test-only'; password = 'test-only' })
        }
        return $config
    }
    function Get-RelayResolvedAddresses {
        param($Address, $TimeoutMs)
        Assert-Equal $script:PageMode 'dns-failure' 'Numeric ingress addresses should not perform DNS resolution.'
        Assert-Equal $Address 'relay.example.invalid' 'Resolve only the configured ingress domain.'
        throw (New-Object TimeoutException('mock ingress DNS timeout'))
    }
    function Get-PortFromJsonFile {
        param($Path, $Fallback)
        Assert-True ($script:PageMode -in @('missing-config', 'invalid-port')) 'Fallback is only expected for absent or invalid saved proxy port.'
        return 10087
    }
    function Invoke-ExitIpLookup {
        param($ProxyAddress)
        Assert-Equal $ProxyAddress ('127.0.0.1:' + $script:PageExpectedSocks) 'Exit IP must use the currently effective local SOCKS port.'
        $script:PageIpCount++
        return [pscustomobject]@{ Success = $script:PageMode -ne 'ip-failure'; Address = '198.51.100.9'; Error = 'mock IP lookup failure' }
    }
    function Measure-LatencySamples {
        [CmdletBinding()]
        param($Address, $Port, $Count, $TimeoutMs)
        Assert-Equal $Address $script:PageExpectedRelay 'Ingress TCP should follow the selected node.'
        Assert-Equal $Port $script:PageExpectedRelayPort 'Ingress TCP should use its current configured transport port.'
        Assert-Equal $Count 3 'Ingress sample count'
        [void]$script:PageCalls.Add('ingress')
        $samples = for ($i = 0; $i -lt 3; $i++) {
            [pscustomobject]@{ Success = $script:PageMode -ne 'ingress-failure'; Milliseconds = 165.0; Error = 'mock ingress TCP failure' }
        }
        return (New-LatencyStats -Samples $samples)
    }
    function Invoke-FullPathTcpSamples {
        [CmdletBinding()]
        param($Socks5Port, $GameAddress, $GamePort, $Count, $TimeoutMs, $WarmupTimeoutMs)
        Assert-Equal $Socks5Port $script:PageExpectedSocks 'The active SOCKS connection alone selects the full-path node.'
        Assert-Equal $GameAddress '51.222.56.192' 'Current configurable game address'
        Assert-Equal $GamePort 8484 'Current configurable game port'
        Assert-Equal $Count 3 'Full-path sample count'
        Assert-Equal $TimeoutMs 2000 'Full-path per-connect timeout'
        Assert-Equal $WarmupTimeoutMs 5000 'Full-path warmup timeout'
        $script:PageFullCount++
        [void]$script:PageCalls.Add('full')
        if ($script:PageMode -eq 'missing-v2') { return (New-UnavailableLatencyResult '节点未提供兼容的版本 2 全程 TCP 测速服务或服务不可达') }
        $samples = for ($i = 0; $i -lt 3; $i++) { [pscustomobject]@{ Success = $true; Milliseconds = 221.25; Error = '' } }
        return (New-LatencyStats -Samples $samples)
    }
    function Merge-TcpLatencySamples { throw 'Full-path game latency must never add independently measured segments.' }
    function Invoke-RelayTcpSamples { throw 'The full-path page must never query the old remote-only timing service.' }
    function Read-GameHandshake { throw 'The full-path page must never time a game greeting.' }
    Check-IpAddress
    return [pscustomobject]@{
        Text = [string]::Join([Environment]::NewLine, $script:PageLines)
        Calls = [string]::Join(',', $script:PageCalls)
        IpCount = $script:PageIpCount; WaitCount = $script:PageWaitCount
        FullCount = $script:PageFullCount; SocksPort = $script:State.Socks5Port
    }
}

Test-Case 'page displays the measured full path without adding ingress TCP or explanatory footnotes' {
    foreach ($mode in @('ready', 'second-node')) {
        $page = Invoke-TestFullPathPage -Mode $mode
        Assert-Equal $page.Calls 'ingress,full' 'Ingress and full-path measurements remain separate.'
        Assert-Equal $page.IpCount 1 'Exit IP query count'
        Assert-Equal $page.WaitCount 1 'Return prompt count'
        Assert-Equal $page.FullCount 1 'One three-sample full-path sequence per page'
        Assert-True ($page.Text -match '(?m)^\s*当前到中转服务器延迟：165 ms\s*$') 'Ingress latency should remain separately visible.'
        Assert-True ($page.Text -match '(?m)^\s*当前到游戏服务器延迟：221 ms\s*$') 'Game latency must be the measured 221 ms, never 165 + 221.'
        Assert-True ($page.Text -notmatch '说明：|经 MSBOOST|不等同于|握手|成功 3/3|395|386') 'Keep the concise result without old metrics or commentary.'
        if ($mode -eq 'second-node') {
            Assert-Equal $page.SocksPort 10088 'Selecting another node must update the effective SOCKS port.'
            Assert-True ($page.Text -match '中转测试目标：192.0.2.20:34069') 'The second ingress must be displayed.'
            Assert-True ($page.Text -notmatch '192.0.2.10') 'Do not retain a fixed Los Angeles node.'
        }
    }
}
Test-Case 'ingress failures UDP multiple nodes and missing config never suppress valid full-path measurement' {
    foreach ($mode in @('ingress-failure', 'dns-failure', 'udp', 'multiple-nodes', 'missing-config', 'invalid-port', 'ip-failure')) {
        $page = Invoke-TestFullPathPage -Mode $mode
        Assert-Equal $page.FullCount 1 ($mode + ' must still run the current SOCKS full-path protocol.')
        Assert-Equal $page.IpCount 1 ($mode + ' keeps independent IP lookup.')
        Assert-Equal $page.WaitCount 1 'Return prompt remains usable.'
        Assert-True ($page.Text -match '(?m)^\s*当前到游戏服务器延迟：221 ms\s*$') ($mode + ' must retain successful full-path timing.')
        if ($mode -in @('dns-failure', 'udp', 'multiple-nodes', 'missing-config')) {
            Assert-Equal $page.Calls 'full' 'Unavailable ingress TCP must not be fabricated or required for game timing.'
        }
        if ($mode -in @('missing-config', 'invalid-port')) { Assert-Equal $page.SocksPort 10087 'Use the established SOCKS fallback when the effective config port is unavailable.' }
        if ($mode -eq 'ip-failure') { Assert-True ($page.Text -match 'mock IP lookup failure') 'IP failure remains visible while latency succeeds.' }
    }
}
Test-Case 'missing v2 service reports the protocol requirement without falling back to an invented game metric' {
    $page = Invoke-TestFullPathPage -Mode 'missing-v2'
    Assert-Equal $page.Calls 'ingress,full' 'Try the v2 service once while retaining separate ingress timing.'
    Assert-True ($page.Text -match '(?m)^\s*当前到中转服务器延迟：165 ms\s*$') 'Independent ingress timing remains available.'
    Assert-True ($page.Text -match '当前出口 IPv4：198\.51\.100\.9') 'Independent IP result remains available.'
    $game = @($page.Text -split '\r?\n' | Where-Object { $_ -match '当前到游戏服务器延迟：' })
    Assert-Equal $game.Count 1 'One explicit game result'
    Assert-True ($game[0] -match '版本 2.*服务') 'A missing or old service needs a clear v2 requirement.'
    Assert-True ($game[0] -notmatch '\d+(?:\.\d+)?\s*ms') 'Do not substitute ingress, game hello, or remote-only latency.'
}
Test-Case 'explicit local authentication blocks unsupported full-path access without blocking ingress or IP status' {
    $page = Invoke-TestFullPathPage -Mode 'authentication'
    Assert-Equal $page.Calls 'ingress' 'Do not attempt a no-auth full-path session with explicit authentication configured.'
    Assert-Equal $page.FullCount 0 'Unsupported local authentication blocks full-path access.'
    Assert-Equal $page.IpCount 1 'IP lookup remains independent.'
    Assert-True ($page.Text -match '当前到游戏服务器延迟：.*认证') 'Authentication requirement remains visible.'
    Assert-Equal $page.WaitCount 1 'Return prompt'
}

Write-Host ('{0} passed; {1} failed. PowerShell {2}' -f $script:Passed, $script:Failed, $PSVersionTable.PSVersion)
if ($script:Failed -gt 0) { exit 1 }
