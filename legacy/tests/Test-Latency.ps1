# Run with Windows PowerShell 5.1; all network activity stays on loopback.
param([string]$LauncherPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'MSBOOST启动器.bat'))

$ErrorActionPreference = 'Stop'
$script:Utf8 = New-Object System.Text.UTF8Encoding($false)
$source = [IO.File]::ReadAllText($LauncherPath, $script:Utf8)
$marker = '#<MSBOOST_PS>'
$markerOffset = $source.IndexOf($marker, [StringComparison]::Ordinal)
if ($markerOffset -lt 0) { throw 'Embedded PowerShell marker was not found.' }
$tokens = $null
$parseErrors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseInput(
    $source.Substring($markerOffset + $marker.Length), [ref]$tokens, [ref]$parseErrors)
if (@($parseErrors).Count -ne 0) { throw ($parseErrors | Out-String) }
# Load definitions only: no launcher initialization, state files, processes, or menu.
$definitions = @($ast.EndBlock.Statements | Where-Object {
    $_ -is [System.Management.Automation.Language.FunctionDefinitionAst]
} | ForEach-Object { $_.Extent.Text })
. ([ScriptBlock]::Create([string]::Join([Environment]::NewLine, $definitions)))
$script:Root = Split-Path -Parent $LauncherPath
$script:ExePath = Join-Path $script:Root 'MSBOOST.exe'
$script:DefaultPort = 1080
$script:GameAddress = '35.155.204.207'
$script:GamePort = 8585
$script:LatencySampleCount = 3
$script:LatencyTimeoutMs = 2000
$script:State = [pscustomobject]@{ ConfigFile = 'unused.json'; Socks5Port = 1080 }
foreach ($name in @('Get-EffectiveConfig', 'Get-LatencyTargets', 'Measure-TcpLatency', 'Measure-LatencySamples', 'Format-LatencyResult')) {
    if (-not (Get-Command $name -CommandType Function -ErrorAction SilentlyContinue)) {
        throw ('Required launcher function is missing: ' + $name)
    }
}

if (-not ('LatencyTest.MockServer' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;

namespace LatencyTest {
    // A real background TCP peer, independent of PowerShell's single runspace.
    public sealed class MockServer : IDisposable {
        private readonly TcpListener listener;
        private readonly Thread thread;
        private readonly object gate = new object();
        private readonly List<TcpClient> clients = new List<TcpClient>();
        private readonly List<string> requests = new List<string>();
        private readonly List<string> faults = new List<string>();
        private volatile bool stopped;
        private int accepted;
        private int disconnected;
        public readonly int Port;
        public string Mode = "socks";
        public string ReplyAddressType = "ipv4";
        public int GreetingDelayMs;
        public int ReplyDelayMs;
        public int FragmentDelayMs;
        public int ResponseCode;
        public int Method;
        public bool Fragment;
        public bool Truncated;
        public bool InvalidVersion;
        public bool InvalidReserved;
        public bool InvalidAddressType;
        public bool EmptyDomain;
        public bool Stall;
        public int[] ResponseCodes;
        public int[] ReplyDelays;
        public string GameHelloMode = "none";
        public string[] GameHelloModes;
        public int GameHelloDelayMs;
        public int[] GameHelloDelays;
        public bool FragmentGameHello;
        public int GameHelloFragmentDelayMs;
        public int Accepted { get { return accepted; } }
        public int Disconnected { get { return disconnected; } }
        public int Active { get { lock (gate) { return clients.Count; } } }
        public string[] Requests { get { lock (gate) { return requests.ToArray(); } } }
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
                    int sample = Interlocked.Increment(ref accepted) - 1;
                    ThreadPool.QueueUserWorkItem(delegate { Handle(client, sample); });
                } catch (SocketException) { if (stopped) return; throw; }
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
        private void Handle(TcpClient client, int sample) {
            try {
                client.NoDelay = true;
                NetworkStream stream = client.GetStream();
                stream.ReadTimeout = 5000;
                stream.WriteTimeout = 5000;
                if (Mode == "direct") { return; }
                byte[] greeting = ReadExactly(stream, 2);
                byte[] methods = ReadExactly(stream, greeting[1]);
                if (greeting[0] != 5 || Array.IndexOf(methods, (byte)0) < 0)
                    throw new InvalidDataException("Expected a SOCKS5 no-auth offer.");
                if (GreetingDelayMs > 0) Thread.Sleep(GreetingDelayMs);
                stream.Write(new byte[] { 5, (byte)Method }, 0, 2);
                if (Method != 0) { return; }
                byte[] header = ReadExactly(stream, 4);
                if (header[0] != 5 || header[1] != 1 || header[2] != 0)
                    throw new InvalidDataException("Expected a SOCKS5 CONNECT request.");
                string address;
                if (header[3] == 1) address = new IPAddress(ReadExactly(stream, 4)).ToString();
                else if (header[3] == 4) address = new IPAddress(ReadExactly(stream, 16)).ToString();
                else if (header[3] == 3) address = Encoding.ASCII.GetString(ReadExactly(stream, ReadExactly(stream, 1)[0]));
                else throw new InvalidDataException("Unknown request address type.");
                byte[] port = ReadExactly(stream, 2);
                lock (gate) { requests.Add(address + ":" + ((port[0] << 8) | port[1])); }
                if (Stall) { while (!stopped) Thread.Sleep(10); return; }
                int delay = ReplyDelays != null ? ReplyDelays[sample % ReplyDelays.Length] : ReplyDelayMs;
                if (delay > 0) Thread.Sleep(delay);
                int response = ResponseCodes != null ? ResponseCodes[sample % ResponseCodes.Length] : ResponseCode;
                List<byte> reply = new List<byte>();
                reply.Add(InvalidVersion ? (byte)4 : (byte)5);
                reply.Add((byte)response);
                reply.Add(InvalidReserved ? (byte)1 : (byte)0);
                if (InvalidAddressType) { reply.Add(99); }
                else if (ReplyAddressType == "ipv6") {
                    reply.Add(4); reply.AddRange(IPAddress.IPv6Loopback.GetAddressBytes());
                } else if (ReplyAddressType == "domain") {
                    byte[] domain = EmptyDomain ? new byte[0] : Encoding.ASCII.GetBytes("mock.local");
                    reply.Add(3); reply.Add((byte)domain.Length); reply.AddRange(domain);
                } else { reply.Add(1); reply.AddRange(IPAddress.Loopback.GetAddressBytes()); }
                reply.Add(0x21); reply.Add(0x89);
                byte[] bytes = reply.ToArray();
                int count = Truncated ? 5 : bytes.Length;
                if (Fragment) {
                    for (int i = 0; i < count; i++) {
                        stream.Write(bytes, i, 1);
                        if (FragmentDelayMs > 0 && i + 1 < count) Thread.Sleep(FragmentDelayMs);
                    }
                } else { stream.Write(bytes, 0, count); }
                if (Truncated) return;
                string helloMode = GameHelloModes != null ? GameHelloModes[sample % GameHelloModes.Length] : GameHelloMode;
                if (response == 0 && helloMode != "none") {
                    int helloDelay = GameHelloDelays != null ? GameHelloDelays[sample % GameHelloDelays.Length] : GameHelloDelayMs;
                    if (helloDelay > 0) Thread.Sleep(helloDelay);
                    // MapleStory v62 hello: LE body length/version/subversion length,
                    // two four-byte IVs, and locale. The client sends no game payload.
                    byte[] hello = new byte[] { 0x0d, 0x00, 0x3e, 0x00, 0x00, 0x00, 0x46, 0x72, 0x7a, 0x84, 0x52, 0x30, 0x78, 0xc1, 0x08 };
                    if (helloMode == "short-length") hello[0] = 12;
                    else if (helloMode == "long-length") { hello[0] = 1; hello[1] = 4; }
                    else if (helloMode == "subversion-length") hello[4] = 1;
                    else if (helloMode == "zero-version") hello[2] = 0;
                    else if (helloMode == "subversion") {
                        hello = new byte[] { 0x0e, 0x00, 0x3e, 0x00, 0x01, 0x00, 0x31, 0x46, 0x72, 0x7a, 0x84, 0x52, 0x30, 0x78, 0xc1, 0x08 };
                    }
                    int helloCount = helloMode == "truncated" ? 7 : hello.Length;
                    if (FragmentGameHello) {
                        for (int i = 0; i < helloCount; i++) {
                            stream.Write(hello, i, 1);
                            if (GameHelloFragmentDelayMs > 0 && i + 1 < helloCount) Thread.Sleep(GameHelloFragmentDelayMs);
                        }
                    } else { stream.Write(hello, 0, helloCount); }
                    if (helloMode == "truncated") return;
                }
                // Every probe must close its connection and must never send game data.
                if (stream.ReadByte() == -1) Interlocked.Increment(ref disconnected);
                else throw new InvalidDataException("Probe unexpectedly sent application data.");
            } catch (IOException) { }
              catch (ObjectDisposedException) { }
              catch (SocketException) { }
              catch (Exception ex) { lock (gate) { faults.Add(ex.Message); } }
            finally {
                client.Close();
                lock (gate) { clients.Remove(client); }
            }
        }
        public void Dispose() {
            stopped = true;
            listener.Stop();
            lock (gate) { foreach (TcpClient client in clients) client.Close(); }
            thread.Join(1000);
        }
    }
}
'@
}

$script:Passed = 0
$script:Failed = 0
function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw $Message }
}
function Assert-Equal {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -ne $Expected) { throw ('{0}: expected <{1}>, got <{2}>' -f $Message, $Expected, $Actual) }
}
function Test-Case {
    param([string]$Name, [scriptblock]$Body)
    try {
        & $Body
        $script:Passed++
        Write-Host ('PASS ' + $Name)
    } catch {
        $script:Failed++
        Write-Host ('FAIL {0}: {1}' -f $Name, $_.Exception.Message) -ForegroundColor Red
    }
}
function New-TestConfig {
    return ('{"activeProfile":"selected","socks5Port":10086,"profiles":[{"profileName":"selected","servers":[{"ipAddress":"192.0.2.50","portBindings":[{"port":443,"protocol":"TCP"}]}]}]}' | ConvertFrom-Json)
}
function Assert-FailedProbe {
    param($Result)
    Assert-True (-not $Result.Success) 'Probe unexpectedly succeeded.'
    Assert-True (-not [string]::IsNullOrWhiteSpace([string]$Result.Error)) 'Failure needs an explanatory error.'
}
function Get-UnusedPort {
    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    try { return ([Net.IPEndPoint]$listener.LocalEndpoint).Port } finally { $listener.Stop() }
}

Test-Case 'describe config output is parsed without running the core' {
    function Invoke-Captured {
        param($Arguments)
        Assert-Equal ($Arguments -join ' ') 'describe config' 'Core command'
        return [pscustomobject]@{ ExitCode = 0; Output = @('status line', '{"socks5Port":10086}', 'done') }
    }
    Assert-Equal (Get-EffectiveConfig).socks5Port 10086 'Effective SOCKS port'
}
Test-Case 'describe config errors and malformed JSON produce no config' {
    function Invoke-Captured { return [pscustomobject]@{ ExitCode = 1; Output = @('{"socks5Port":10086}') } }
    Assert-True ($null -eq (Get-EffectiveConfig)) 'Nonzero describe exit must fail.'
    function Invoke-Captured { return [pscustomobject]@{ ExitCode = 0; Output = @('{invalid json}') } }
    Assert-True ($null -eq (Get-EffectiveConfig)) 'Malformed JSON must fail.'
}
Test-Case 'selected profile resolves a unique TCP endpoint and local SOCKS port' {
    $config = New-TestConfig
    $config.profiles = @([pscustomobject]@{ profileName = 'unused'; servers = @() }) + @($config.profiles)
    $targets = Get-LatencyTargets -Config $config
    Assert-Equal $targets.RelayAddress '192.0.2.50' 'Relay address'
    Assert-Equal $targets.RelayPort 443 'Relay port'
    Assert-Equal $targets.Socks5Port 10086 'SOCKS port'
    Assert-True ([string]::IsNullOrEmpty($targets.RelayError)) 'Valid relay was rejected.'
    Assert-True ([string]::IsNullOrEmpty($targets.GameError)) 'Default handshake was rejected.'
}
Test-Case 'domain relay and a single TCP binding alongside UDP are supported' {
    $config = New-TestConfig
    $server = $config.profiles[0].servers[0]
    $server.PSObject.Properties.Remove('ipAddress')
    $server | Add-Member NoteProperty domainName 'relay.example.invalid'
    $server.portBindings += [pscustomobject]@{ port = 8443; protocol = 'UDP' }
    $targets = Get-LatencyTargets -Config $config
    Assert-Equal $targets.RelayAddress 'relay.example.invalid' 'Domain address'
    Assert-Equal $targets.RelayPort 443 'Single TCP port'
    Assert-True ([string]::IsNullOrEmpty($targets.RelayError)) 'Single TCP binding was rejected.'
}
Test-Case 'missing config and unmatched/duplicate active profiles are rejected' {
    $targets = Get-LatencyTargets -Config $null
    Assert-True (-not [string]::IsNullOrEmpty($targets.RelayError)) 'Missing config needs relay error.'
    Assert-True (-not [string]::IsNullOrEmpty($targets.GameError)) 'Missing config needs game error.'
    foreach ($mode in @('missing', 'duplicate')) {
        $config = New-TestConfig
        if ($mode -eq 'missing') { $config.activeProfile = 'absent' }
        else { $config.profiles += $config.profiles[0] }
        $targets = Get-LatencyTargets -Config $config
        Assert-True (-not [string]::IsNullOrEmpty($targets.RelayError)) ($mode + ' profile needs relay error.')
        Assert-True (-not [string]::IsNullOrEmpty($targets.GameError)) ($mode + ' profile needs game error.')
    }
}
Test-Case 'multiple servers or TCP bindings and port ranges are ambiguous' {
    foreach ($mode in @('servers', 'bindings', 'range')) {
        $config = New-TestConfig
        $profile = $config.profiles[0]
        if ($mode -eq 'servers') { $profile.servers += $profile.servers[0] }
        elseif ($mode -eq 'bindings') { $profile.servers[0].portBindings += [pscustomobject]@{ port = 8443; protocol = 'TCP' } }
        else {
            $binding = $profile.servers[0].portBindings[0]
            $binding.PSObject.Properties.Remove('port')
            $binding | Add-Member NoteProperty portRange '443-445'
        }
        $targets = Get-LatencyTargets -Config $config
        Assert-True ($targets.RelayError -match '无法唯一确定测试目标') ($mode + ' needs ambiguous-target error.')
        Assert-True ([string]::IsNullOrEmpty($targets.GameError)) 'Relay ambiguity must not suppress game measurement.'
    }
}
Test-Case 'UDP-only profile has no relay TCP test but permits proxy measurement' {
    $config = New-TestConfig
    $config.profiles[0].servers[0].portBindings[0].protocol = 'UDP'
    $targets = Get-LatencyTargets -Config $config
    Assert-True ($targets.RelayError -match '无 TCP 测试端口') 'UDP-only explanation'
    Assert-True ([string]::IsNullOrEmpty($targets.GameError)) 'UDP proxy transport still permits TCP CONNECT.'
}
Test-Case 'handshake modes do not block server-reported TCP samples' {
    foreach ($mode in @('HANDSHAKE_DEFAULT', 'HANDSHAKE_STANDARD', 'HANDSHAKE_NO_WAIT', 'FUTURE_MODE')) {
        $config = New-TestConfig
        $config.profiles[0] | Add-Member NoteProperty handshakeMode $mode
        $targets = Get-LatencyTargets -Config $config
        Assert-True ([string]::IsNullOrEmpty($targets.GameError)) ($mode + ' should allow actual server-reported TCP samples.')
        Assert-True ([string]::IsNullOrEmpty($targets.RelayError)) 'Handshake mode must not affect direct relay test.'
    }
}
Test-Case 'configured local authentication and invalid SOCKS ports disable game test' {
    $config = New-TestConfig
    $config | Add-Member NoteProperty socks5Authentication @([pscustomobject]@{ user = 'test'; password = 'test-only' })
    Assert-True ((Get-LatencyTargets -Config $config).GameError -match '认证') 'Configured auth needs explanation.'
    foreach ($port in @(0, -1, 65536, 'bad')) {
        $config = New-TestConfig
        $config.socks5Port = $port
        Assert-True (-not [string]::IsNullOrEmpty((Get-LatencyTargets -Config $config).GameError)) 'Invalid SOCKS port must block measurement.'
    }
}

Test-Case 'direct TCP succeeds on a loopback listener' {
    $server = New-Object LatencyTest.MockServer
    $server.Mode = 'direct'
    try {
        $result = Measure-TcpLatency -Address '127.0.0.1' -Port $server.Port -TimeoutMs 1000
        Assert-True $result.Success 'Direct TCP connection failed.'
        Assert-True ($result.Milliseconds -ge 0) 'Elapsed time must be non-negative.'
    } finally { $server.Dispose() }
}
Test-Case 'direct refusal and an absent proxy return explanatory failures' {
    $port = Get-UnusedPort
    Assert-FailedProbe (Measure-TcpLatency -Address '127.0.0.1' -Port $port -TimeoutMs 400)
    $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $port -TimeoutMs 400
    Assert-FailedProbe $result
}
Test-Case 'SOCKS CONNECT requests the game target and waits for the remote result' {
    $server = New-Object LatencyTest.MockServer
    $server.ReplyDelayMs = 180
    try {
        $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 1000
        Assert-True $result.Success 'SOCKS CONNECT failed.'
        Assert-True ($result.Milliseconds -ge 160) 'Only the local proxy/greeting was timed.'
        Assert-Equal $server.Requests[0] '35.155.204.207:8585' 'SOCKS destination'
        Assert-Equal $server.Faults.Count 0 'Mock protocol errors'
    } finally { $server.Dispose() }
}
Test-Case 'fragmented IPv4, domain, and IPv6 reply addresses are read in full' {
    foreach ($addressType in @('ipv4', 'domain', 'ipv6')) {
        $server = New-Object LatencyTest.MockServer
        $server.ReplyAddressType = $addressType
        $server.Fragment = $true
        $server.FragmentDelayMs = 8
        try {
            $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 1000
            Assert-True $result.Success ($addressType + ' fragmented reply failed.')
            $minimum = @{ ipv4 = 55; domain = 90; ipv6 = 140 }[$addressType]
            Assert-True ($result.Milliseconds -ge $minimum) ($addressType + ' reply was not fully consumed.')
        } finally { $server.Dispose() }
    }
}
Test-Case 'SOCKS requests support domain and IPv6 destinations without local DNS' {
    foreach ($destination in @('game.example.invalid', '2001:db8::5')) {
        $server = New-Object LatencyTest.MockServer
        try {
            $result = Measure-TcpLatency -Address $destination -Port 8585 -Socks5Port $server.Port -TimeoutMs 1000
            Assert-True $result.Success 'Address encoding failed.'
            Assert-Equal $server.Requests[0] ($destination + ':8585') 'Encoded destination'
        } finally { $server.Dispose() }
    }
}
Test-Case 'game measurement waits beyond early SOCKS success for a complete Maple hello' {
    $server = New-Object LatencyTest.MockServer
    $server.GameHelloMode = 'valid'
    $server.GameHelloDelayMs = 180
    try {
        $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 1000 -WaitForGameHandshake
        Assert-True $result.Success 'Valid Maple hello should complete the game probe.'
        Assert-True ($result.Milliseconds -ge 160) 'Probe stopped at the optimistic SOCKS success reply.'
        Assert-Equal $server.Requests[0] '35.155.204.207:8585' 'Game destination'
        Assert-Equal $server.Faults.Count 0 'Mock protocol errors'
    } finally { $server.Dispose() }
}
Test-Case 'fragmented game hello is fully consumed including both IVs and locale' {
    foreach ($helloMode in @('valid', 'subversion')) {
        $server = New-Object LatencyTest.MockServer
        $server.GameHelloMode = $helloMode
        $server.FragmentGameHello = $true
        $server.GameHelloFragmentDelayMs = 20
        try {
            $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 1200 -WaitForGameHandshake
            Assert-True $result.Success ($helloMode + ' fragmented hello failed.')
            Assert-True ($result.Milliseconds -ge 250) 'Probe stopped at the first byte or incomplete hello.'
        } finally { $server.Dispose() }
    }
}
Test-Case 'SOCKS success without a game hello times out instead of claiming game latency' {
    $server = New-Object LatencyTest.MockServer
    try {
        $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 220 -WaitForGameHandshake
        Assert-FailedProbe $result
        Assert-True ($result.Error -match '超时|握手') 'Missing game hello needs an explanatory error.'
    } finally { $server.Dispose() }
}
Test-Case 'malformed and truncated game hellos are never accepted as timing samples' {
    foreach ($helloMode in @('short-length', 'long-length', 'subversion-length', 'zero-version', 'truncated')) {
        $server = New-Object LatencyTest.MockServer
        $server.GameHelloMode = $helloMode
        try {
            $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 600 -WaitForGameHandshake
            Assert-FailedProbe $result
        } finally { $server.Dispose() }
    }
}
Test-Case 'game hello shares the original deadline with every SOCKS phase' {
    $server = New-Object LatencyTest.MockServer
    $server.GreetingDelayMs = 110
    $server.ReplyDelayMs = 110
    $server.GameHelloMode = 'valid'
    $server.GameHelloDelayMs = 250
    try {
        $timer = [Diagnostics.Stopwatch]::StartNew()
        $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 340 -WaitForGameHandshake
        $timer.Stop()
        Assert-FailedProbe $result
        Assert-True ($result.Error -match '超时') 'Overall handshake deadline needs a timeout explanation.'
        Assert-True ($timer.ElapsedMilliseconds -lt 460) 'The deadline restarted after SOCKS CONNECT.'
    } finally { $server.Dispose() }
}
Test-Case 'slow fragmented game hello cannot restart the deadline on every read' {
    $server = New-Object LatencyTest.MockServer
    $server.GameHelloMode = 'valid'
    $server.FragmentGameHello = $true
    $server.GameHelloFragmentDelayMs = 70
    try {
        $timer = [Diagnostics.Stopwatch]::StartNew()
        $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 300 -WaitForGameHandshake
        $timer.Stop()
        Assert-FailedProbe $result
        Assert-True ($timer.ElapsedMilliseconds -lt 650) 'The deadline restarted after a short game-hello read.'
    } finally { $server.Dispose() }
}
Test-Case 'SOCKS authentication requirements and rejected methods fail explicitly' {
    foreach ($method in @(2, 255)) {
        $server = New-Object LatencyTest.MockServer
        $server.Method = $method
        try {
            $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 600
            Assert-FailedProbe $result
            if ($method -eq 2) { Assert-True ($result.Error -match '认证') 'Authentication explanation missing.' }
            Assert-Equal $server.Requests.Count 0 'CONNECT must not be sent after method rejection.'
        } finally { $server.Dispose() }
    }
}
Test-Case 'SOCKS remote refusal, malformed headers, and truncated replies fail' {
    foreach ($mode in @('refused', 'version', 'reserved', 'atyp', 'eof', 'empty-domain')) {
        $server = New-Object LatencyTest.MockServer
        if ($mode -eq 'refused') { $server.ResponseCode = 5 }
        elseif ($mode -eq 'version') { $server.InvalidVersion = $true }
        elseif ($mode -eq 'reserved') { $server.InvalidReserved = $true }
        elseif ($mode -eq 'atyp') { $server.InvalidAddressType = $true }
        elseif ($mode -eq 'eof') { $server.Truncated = $true }
        else { $server.ReplyAddressType = 'domain'; $server.EmptyDomain = $true }
        try { Assert-FailedProbe (Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 600) }
        finally { $server.Dispose() }
    }
}
Test-Case 'timeout budget is shared by greeting and remote-connect phases' {
    $server = New-Object LatencyTest.MockServer
    $server.GreetingDelayMs = 210
    $server.ReplyDelayMs = 210
    try {
        $timer = [Diagnostics.Stopwatch]::StartNew()
        $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 340
        $timer.Stop()
        Assert-FailedProbe $result
        Assert-True ($result.Error -match '超时') 'Timeout explanation missing.'
        Assert-True ($timer.ElapsedMilliseconds -lt 600) 'Deadline was restarted for each protocol phase.'
    } finally { $server.Dispose() }
}
Test-Case 'slow fragmented reply cannot restart the timeout on every read' {
    $server = New-Object LatencyTest.MockServer
    $server.Fragment = $true
    $server.FragmentDelayMs = 85
    try {
        $timer = [Diagnostics.Stopwatch]::StartNew()
        $result = Measure-TcpLatency -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 280
        $timer.Stop()
        Assert-FailedProbe $result
        Assert-True ($timer.ElapsedMilliseconds -lt 600) 'Deadline restarted after each short read.'
    } finally { $server.Dispose() }
}
Test-Case 'three probes average successful samples and preserve failure count' {
    $server = New-Object LatencyTest.MockServer
    $server.ResponseCodes = [int[]]@(0, 5, 0)
    $server.ReplyDelays = [int[]]@(60, 350, 140)
    try {
        $result = Measure-LatencySamples -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 800
        Assert-Equal $result.TotalCount 3 'Default sample count'
        Assert-Equal $result.SuccessCount 2 'Successful sample count'
        Assert-Equal $server.Accepted 3 'Exactly three connections'
        Assert-True ($result.AverageMs -ge 85 -and $result.AverageMs -lt 170) 'Average must exclude the slow refused sample.'
        Assert-True (@($result.Errors).Count -ge 1) 'Failure reasons were lost.'
        $formatted = Format-LatencyResult -Result $result
        Assert-True ($formatted -match 'ms' -and $formatted -match '2\s*/\s*3') 'Display must include milliseconds and success count.'
    } finally { $server.Dispose() }
}
Test-Case 'all-failed measurement shows a reason instead of zero milliseconds' {
    $server = New-Object LatencyTest.MockServer
    $server.ResponseCode = 5
    try {
        $result = Measure-LatencySamples -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 600
        Assert-Equal $result.SuccessCount 0 'All failures'
        Assert-Equal $result.TotalCount 3 'Total count'
        $formatted = Format-LatencyResult -Result $result
        Assert-True (-not [string]::IsNullOrEmpty($formatted)) 'Empty failure display'
        Assert-True ($formatted -notmatch '(^|\D)0(?:\.0+)?\s*ms') 'Failure must not be shown as zero latency.'
        Assert-True ($formatted -match '拒绝|失败') 'Failure display needs a reason.'
    } finally { $server.Dispose() }
}
Test-Case 'three game samples count only complete hellos and average successful timings' {
    $server = New-Object LatencyTest.MockServer
    $server.GameHelloModes = [string[]]@('valid', 'none', 'valid')
    $server.GameHelloDelays = [int[]]@(70, 0, 130)
    try {
        $result = Measure-LatencySamples -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 350 -WaitForGameHandshake
        Assert-Equal $result.TotalCount 3 'Default game sample count'
        Assert-Equal $result.SuccessCount 2 'Only full hellos count as successful game samples.'
        Assert-Equal $server.Accepted 3 'Exactly three game connections'
        Assert-True ($result.AverageMs -ge 90 -and $result.AverageMs -lt 180) 'Game average must exclude the timed-out sample.'
        Assert-True (@($result.Errors).Count -ge 1) 'Missing-hello failure reason was lost.'
        $formatted = Format-LatencyResult -Result $result
        Assert-True ($formatted -match 'ms' -and $formatted -match '2\s*/\s*3') 'Game result must include successful sample count.'
    } finally { $server.Dispose() }
}
Test-Case 'all missing game hellos show a failure rather than an optimistic SOCKS time' {
    $server = New-Object LatencyTest.MockServer
    try {
        $result = Measure-LatencySamples -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 120 -WaitForGameHandshake
        Assert-Equal $result.SuccessCount 0 'SOCKS success alone must never count as a game sample.'
        Assert-Equal $result.TotalCount 3 'Game attempt count'
        $formatted = Format-LatencyResult -Result $result
        Assert-True ($formatted -match '超时|握手|失败') 'Game failure display needs an explanation.'
        Assert-True ($formatted -notmatch '(^|\D)\d+(?:\.\d+)?\s*ms') 'No game timing may be displayed without a hello.'
    } finally { $server.Dispose() }
}
Test-Case 'repeated successful measurements release all proxy sockets' {
    $server = New-Object LatencyTest.MockServer
    try {
        for ($i = 0; $i -lt 3; $i++) {
            $result = Measure-LatencySamples -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 600
            Assert-Equal $result.SuccessCount 3 'Repeated sample results'
        }
        $deadline = [DateTime]::UtcNow.AddSeconds(2)
        while ($server.Disconnected -lt 9 -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 10 }
        Assert-Equal $server.Accepted 9 'Expected socket count'
        Assert-Equal $server.Disconnected 9 'Each successful connection must be closed.'
        Assert-Equal $server.Faults.Count 0 'Mock protocol errors'
    } finally { $server.Dispose() }
}
Test-Case 'game probes release sockets after valid, absent, and malformed hellos' {
    $server = New-Object LatencyTest.MockServer
    $server.GameHelloModes = [string[]]@('valid', 'none', 'short-length')
    try {
        for ($i = 0; $i -lt 3; $i++) {
            $result = Measure-LatencySamples -Address '35.155.204.207' -Port 8585 -Socks5Port $server.Port -TimeoutMs 150 -WaitForGameHandshake
            Assert-Equal $result.SuccessCount 1 'Only the valid hello should succeed.'
        }
        $deadline = [DateTime]::UtcNow.AddSeconds(2)
        while ($server.Active -gt 0 -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 10 }
        Assert-Equal $server.Accepted 9 'Game probe socket count'
        Assert-Equal $server.Active 0 'Successful and failed game probes must close every socket.'
        Assert-Equal $server.Faults.Count 0 'Probes must not send application data.'
    } finally { $server.Dispose() }
}

# Page-only mocks: retain all protocol tests above and exercise the real result merge.
function Invoke-TestLatencyPage {
    param([string]$Mode = 'ready', [string]$Failure = 'none')
    $script:PageMode = $Mode
    $script:PageFailureTarget = $Failure
    $script:RelayProbeEnabled = $Mode -ne 'disabled'
    $script:RelayProbePort = 20424
    $otherNode = $Mode -eq 'alternate-relay' -or $Mode -like 'domain-*'
    $script:PageRelayAddress = if ($otherNode) { '192.0.2.20' } else { '192.0.2.10' }
    $script:PageRelayPort = if ($otherNode) { 34069 } else { 443 }
    $script:PageSocksPort = if ($otherNode) { 10088 } else { 10086 }
    $script:PageLines = New-Object 'Collections.Generic.List[string]'
    $script:PageMeasurements = New-Object 'Collections.Generic.List[string]'
    $script:PageRemoteTargets = New-Object 'Collections.Generic.List[string]'
    $script:PageWaitCount = 0
    $script:PageSaveCount = 0
    $script:PageIpCount = 0
    $script:State = [pscustomobject]@{ ConfigFile = 'unused.json'; Socks5Port = 1080 }
    function Set-ScreenColor { param($Color) }
    function Clear-Host {}
    function Write-Header { param($Title) }
    function Write-Host { param($Object, $ForegroundColor) [void]$script:PageLines.Add([string]$Object) }
    function Wait-ForMenu { $script:PageWaitCount++ }
    function Save-State { $script:PageSaveCount++; return $true }
    function Get-EffectiveConfig {
        if ($script:PageMode -eq 'missing') { return $null }
        $config = New-TestConfig
        $config.profiles[0].servers[0].ipAddress = $script:PageRelayAddress
        $config.profiles[0].servers[0].portBindings[0].port = $script:PageRelayPort
        $config.socks5Port = $script:PageSocksPort
        if ($script:PageMode -like 'domain-*') {
            $config.profiles[0].servers[0] | Add-Member NoteProperty domainName 'relay.example.invalid'
        }
        if ($script:PageMode -eq 'no-wait') { $config.profiles[0] | Add-Member NoteProperty handshakeMode 'HANDSHAKE_NO_WAIT' }
        if ($script:PageMode -eq 'authentication') {
            $config | Add-Member NoteProperty socks5Authentication @([pscustomobject]@{ user = 'test'; password = 'test-only' })
        }
        if ($script:PageMode -eq 'invalid-port') { $config.socks5Port = 0 }
        return $config
    }
    function Get-RelayResolvedAddresses {
        param($Address, $TimeoutMs)
        Assert-Equal $Address 'relay.example.invalid' 'Resolve only the domain from the active relay configuration.'
        Assert-Equal $TimeoutMs 2000 'Relay DNS resolution must use the bounded 2-second timeout.'
        switch ($script:PageMode) {
            'domain-single' { return @([Net.IPAddress]::Parse('192.0.2.20')) }
            'domain-multiple' { return @([Net.IPAddress]::Parse('192.0.2.20'), [Net.IPAddress]::Parse('192.0.2.10')) }
            'domain-ipv6' { return @([Net.IPAddress]::Parse('2001:db8::1')) }
            'domain-timeout' { throw (New-Object TimeoutException('mock DNS timeout')) }
            default { throw 'A numeric relay must not trigger DNS resolution.' }
        }
    }
    function Get-PortFromJsonFile {
        param($Path, $Fallback)
        Assert-True ($script:PageMode -in @('missing', 'invalid-port')) 'Unexpected fallback configuration read.'
        Assert-Equal $Path (Join-Path $script:Root 'unused.json') 'Fallback must use the selected configuration.'
        Assert-Equal $Fallback 1080 'Fallback must receive the existing state port.'
        return 10087
    }
    function Invoke-ExitIpLookup {
        param($ProxyAddress)
        $expectedPort = if ($script:PageMode -in @('missing', 'invalid-port')) { 10087 } else { $script:PageSocksPort }
        Assert-Equal $ProxyAddress ('127.0.0.1:' + $expectedPort) 'Exit-IP query must use the resolved proxy port.'
        $script:PageIpCount++
        return [pscustomobject]@{ Success = $script:PageFailureTarget -notin @('ip', 'both'); Address = '198.51.100.7'; Error = 'mock IP failure' }
    }
    function Measure-TcpLatency { throw 'The page must not call a separate SOCKS/game-handshake timing probe.' }
    function Measure-LatencySamples {
        param($Address, $Port, $Socks5Port = 0, $Count, $TimeoutMs, [switch]$WaitForGameHandshake)
        Assert-Equal $Count 3 'Page sample count'
        Assert-Equal $TimeoutMs 2000 'Page per-attempt timeout'
        Assert-Equal $Socks5Port 0 'Relay latency must use direct TCP, not SOCKS.'
        Assert-True (-not $WaitForGameHandshake) 'The page must not measure a game hello as TCP latency.'
        Assert-Equal $Address $script:PageRelayAddress 'Direct relay address must follow the active configuration.'
        Assert-Equal $Port $script:PageRelayPort 'Direct relay TCP port must follow the active configuration.'
        [void]$script:PageMeasurements.Add('relay')
        $samples = for ($i = 0; $i -lt 3; $i++) {
            $ok = $script:PageFailureTarget -notin @('relay', 'both')
            if ($script:PageFailureTarget -eq 'paired' -and $i -eq 0) { $ok = $false }
            [pscustomobject]@{ Success = $ok; Milliseconds = $(if ($ok) { 155 + 10 * $i } else { $null }); Error = $(if ($ok) { '' } else { 'mock relay failure' }) }
        }
        return (New-LatencyStats -Samples $samples)
    }
    function Invoke-RelayTcpSamples {
        param($RelayAddress, $ProbePort, $GameAddress, $GamePort, $Socks5Port, $TimeoutMs)
        Assert-True ($script:PageMode -in @('ready', 'no-wait', 'alternate-relay', 'domain-single')) 'Unsupported configuration must not contact the relay probe service.'
        Assert-Equal ($script:PageMeasurements -join ',') 'relay' 'Measure the direct relay before requesting remote samples.'
        Assert-Equal $RelayAddress $script:PageRelayAddress 'Remote service must belong to the active relay; never fall back to the previous node.'
        Assert-Equal $ProbePort 20424 'Configured relay probe service port'
        Assert-Equal $GameAddress '35.155.204.207' 'Configured fixed game address'
        Assert-Equal $GamePort 8585 'Configured fixed game port'
        Assert-Equal $Socks5Port $script:PageSocksPort 'Remote request must traverse the active configuration local SOCKS port.'
        [void]$script:PageMeasurements.Add('remote')
        [void]$script:PageRemoteTargets.Add(('{0}:{1} via 127.0.0.1:{2}' -f $RelayAddress, $ProbePort, $Socks5Port))
        if ($script:PageFailureTarget -eq 'service') {
            return (New-UnavailableLatencyResult ('当前中转测速服务未部署、未启动或不可达（端口 {0}）：mock service unavailable' -f $ProbePort))
        }
        $samples = for ($i = 0; $i -lt 3; $i++) {
            $ok = $script:PageFailureTarget -ne 'remote'
            if ($script:PageFailureTarget -eq 'paired' -and $i -eq 1) { $ok = $false }
            [pscustomobject]@{ Success = $ok; Milliseconds = $(if ($ok) { 50 + 10 * $i } else { $null }); Error = $(if ($ok) { '' } else { 'mock remote failure' }) }
        }
        return (New-LatencyStats -Samples $samples)
    }
    Check-IpAddress
    return [pscustomobject]@{
        Text = [string]::Join([Environment]::NewLine, $script:PageLines)
        Calls = [string]::Join(',', $script:PageMeasurements)
        IpCount = $script:PageIpCount
        WaitCount = $script:PageWaitCount
        SaveCount = $script:PageSaveCount
        SocksPort = $script:State.Socks5Port
        RemoteTargets = [string]::Join(',', $script:PageRemoteTargets)
    }
}

Test-Case 'numeric relay resolution bypasses DNS and rejects unsupported IPv6' {
    function Get-RelayResolvedAddresses { throw 'A numeric address must not perform DNS resolution.' }
    $ipv4 = Resolve-LatencyRelayAddress -Address '192.0.2.20' -TimeoutMs 2000
    Assert-Equal $ipv4.Address '192.0.2.20' 'The configured IPv4 address must be retained.'
    Assert-True ([string]::IsNullOrEmpty($ipv4.Error)) 'A valid IPv4 address must be accepted.'
    $ipv6 = Resolve-LatencyRelayAddress -Address '2001:db8::1' -TimeoutMs 2000
    Assert-True (-not [string]::IsNullOrEmpty($ipv6.Error)) 'The IPv4-only probe must explicitly reject an IPv6 relay.'
    Assert-True ([string]::IsNullOrEmpty($ipv6.Address)) 'Unsupported IPv6 must not become a probe address.'
}
Test-Case 'enabled page adds actual relay and remote TCP samples and keeps concise output' {
    $page = Invoke-TestLatencyPage
    Assert-Equal $page.Calls 'relay,remote' 'Both TCP segments must be measured.'
    Assert-Equal $page.IpCount 1 'Exit IP should be queried once.'
    Assert-Equal $page.WaitCount 1 'Page must return through the existing Enter prompt once.'
    Assert-Equal $page.SaveCount 1 'Effective proxy port should update the existing state once.'
    Assert-Equal $page.SocksPort 10086 'Effective proxy port should synchronize to state.'
    Assert-True ($page.Text -match 'SOCKS5 地址：127\.0\.0\.1:10086') 'Effective SOCKS address must remain visible.'
    Assert-True ($page.Text -match '中转测试目标：192.0.2.10:443') 'Relay target must remain visible.'
    Assert-True ($page.Text -match '游戏测试目标：35\.155\.204\.207:8585') 'Configured game target must remain visible.'
    Assert-True ($page.Text -match '当前出口 IPv4：198\.51\.100\.7') 'Successful exit IP must remain visible.'
    Assert-True ($page.Text -match '(?m)^\s*当前到中转服务器延迟：165 ms\s*$') 'Relay line should show only its integer TCP latency.'
    Assert-True ($page.Text -match '(?m)^\s*当前到游戏服务器延迟：225 ms\s*$') 'Game line should show the two TCP segment sum, 165 + 60.'
    Assert-True ($page.Text -match '正在测量 TCP 延迟') 'Progress should describe TCP measurement.'
    Assert-True ($page.Text -notmatch '说明：|握手响应耗时|不等同于|包含代理|经 MSBOOST|平均 TCP|建连耗时|成功 3/3') 'Explanatory text and sample counts must not appear.'
}
Test-Case 'IP and both TCP segment failures remain independent on page 3' {
    foreach ($failedTarget in @('ip', 'relay', 'remote', 'both')) {
        $page = Invoke-TestLatencyPage -Failure $failedTarget
        $expectedCalls = if ($failedTarget -in @('relay', 'both')) { 'relay' } else { 'relay,remote' }
        Assert-Equal $page.Calls $expectedCalls 'IP failure must not suppress TCP probes; all failed relay samples cannot yield a combined result.'
        Assert-Equal $page.IpCount 1 'IP lookup must run once even when latency fails.'
        Assert-Equal $page.WaitCount 1 'Return prompt must remain available.'
        if ($failedTarget -in @('ip', 'both')) {
            Assert-True ($page.Text -match 'mock IP failure') 'IP failure reason must stay visible.'
            Assert-True ($page.Text -notmatch '\[成功\]') 'IP lookup must not claim success.'
        } else {
            Assert-True ($page.Text -match '198\.51\.100\.7') 'Successful exit IP must remain visible.'
        }
        if ($failedTarget -eq 'ip') {
            Assert-True ($page.Text -match '当前到中转服务器延迟：165 ms') 'IP failure must not suppress relay latency.'
            Assert-True ($page.Text -match '当前到游戏服务器延迟：225 ms') 'IP failure must not suppress game TCP latency.'
        } else {
            $gameLine = @($page.Text -split '\r?\n' | Where-Object { $_ -match '当前到游戏服务器延迟：' })
            Assert-Equal $gameLine.Count 1 'Game result line must remain visible.'
            Assert-True ($gameLine[0] -notmatch '\d+(?:\.\d+)?\s*ms') 'A failed segment must not become a fabricated game latency.'
            $expectedReason = if ($failedTarget -eq 'remote') { 'mock remote failure' } else { 'mock relay failure' }
            Assert-True ($gameLine[0] -match [regex]::Escape($expectedReason)) 'The failing segment should supply the game result reason.'
        }
    }
}
Test-Case 'page sums each TCP segment successful average without arbitrary sample pairing' {
    $page = Invoke-TestLatencyPage -Failure 'paired'
    Assert-Equal $page.Calls 'relay,remote' 'Both sample collections must be available.'
    Assert-True ($page.Text -match '(?m)^\s*当前到中转服务器延迟：170 ms\s*$') 'The relay average must use its successful samples, 165 and 175.'
    Assert-True ($page.Text -match '(?m)^\s*当前到游戏服务器延迟：230 ms\s*$') 'The game result must sum the independently measured means: (165 + 175) / 2 + (50 + 70) / 2 = 230.'
    $relayMatch = [regex]::Match($page.Text, '当前到中转服务器延迟：(\d+) ms')
    $gameMatch = [regex]::Match($page.Text, '当前到游戏服务器延迟：(\d+) ms')
    Assert-True ($relayMatch.Success -and $gameMatch.Success) 'Both numeric TCP results must remain visible.'
    Assert-True ([int]$gameMatch.Groups[1].Value -ge [int]$relayMatch.Groups[1].Value) 'Summing nonnegative segment means must never make game latency lower than the displayed relay mean.'
}
Test-Case 'HANDSHAKE_NO_WAIT permits server-reported TCP results without game handshake timing' {
    $page = Invoke-TestLatencyPage -Mode 'no-wait'
    Assert-Equal $page.Calls 'relay,remote' 'Actual remote JSON samples remain valid under early SOCKS success.'
    Assert-True ($page.Text -match '当前到游戏服务器延迟：225 ms') 'NO_WAIT must still show the measured two-segment sum.'
    Assert-True ($page.Text -notmatch '说明：|握手|不等同于|经 MSBOOST') 'No obsolete handshake restriction should be shown.'
    Assert-Equal $page.WaitCount 1 'Return prompt'
}
Test-Case 'switching nodes updates both TCP destinations and the local SOCKS port without a fixed relay binding' {
    $original = Invoke-TestLatencyPage
    Assert-Equal $original.RemoteTargets '192.0.2.10:20424 via 127.0.0.1:10086' 'The original node must use its own service and SOCKS port.'
    $page = Invoke-TestLatencyPage -Mode 'alternate-relay'
    Assert-Equal $page.Calls 'relay,remote' 'Both TCP segments must be measured after switching to another node.'
    Assert-Equal $page.RemoteTargets '192.0.2.20:20424 via 127.0.0.1:10088' 'The new relay service must receive the request through the new SOCKS port.'
    Assert-Equal $page.SocksPort 10088 'The newly selected SOCKS port must synchronize to state.'
    Assert-Equal $page.SaveCount 1 'The newly selected SOCKS port must be saved once.'
    Assert-Equal $page.IpCount 1 'Exit IP should use the new configuration once.'
    Assert-Equal $page.WaitCount 1 'Return prompt must remain available after switching nodes.'
    Assert-True ($page.Text -match 'SOCKS5 地址：127\.0\.0\.1:10088') 'The current local SOCKS port must be displayed.'
    Assert-True ($page.Text -match '中转测试目标：192.0.2.20:34069') 'The selected relay TCP address and transport port must be displayed.'
    Assert-True ($page.Text -match '(?m)^\s*当前到中转服务器延迟：165 ms\s*$') 'The new node must keep its direct TCP result.'
    Assert-True ($page.Text -match '(?m)^\s*当前到游戏服务器延迟：225 ms\s*$') 'The new node must support the same two-segment sum.'
    Assert-True ($page.Text -notmatch '192.0.2.10|未配置|未部署') 'A working new node must not inherit the previous node or its unsupported status.'
    $restored = Invoke-TestLatencyPage
    Assert-Equal $restored.RemoteTargets '192.0.2.10:20424 via 127.0.0.1:10086' 'Switching back must not retain the second node or SOCKS port.'
}
Test-Case 'missing probe service on a newly selected node preserves IP and direct TCP without falling back to another node' {
    $page = Invoke-TestLatencyPage -Mode 'alternate-relay' -Failure 'service'
    Assert-Equal $page.Calls 'relay,remote' 'The current node service should be requested once after direct TCP measurement.'
    Assert-Equal $page.RemoteTargets '192.0.2.20:20424 via 127.0.0.1:10088' 'An unavailable current-node service must never fall back to Los Angeles.'
    Assert-Equal $page.IpCount 1 'Exit IP must remain independent of an unavailable node service.'
    Assert-Equal $page.WaitCount 1 'The page must still offer one return prompt.'
    Assert-True ($page.Text -match '当前出口 IPv4：198\.51\.100\.7') 'Successful IP lookup must remain visible.'
    Assert-True ($page.Text -match '(?m)^\s*当前到中转服务器延迟：165 ms\s*$') 'Successful direct TCP must remain visible.'
    $gameLine = @($page.Text -split '\r?\n' | Where-Object { $_ -match '当前到游戏服务器延迟：' })
    Assert-Equal $gameLine.Count 1 'An unavailable node service still needs one game result line.'
    Assert-True ($page.Text -match '中转测试目标：192.0.2.20:34069') 'The active relay must be visible alongside its unavailable service result.'
    Assert-True ($gameLine[0] -match '当前中转测速服务未部署、未启动或不可达.*20424') 'The game result must explain the current relay service requirement and probe port.'
    Assert-True ($gameLine[0] -notmatch '\d+(?:\.\d+)?\s*ms') 'An unavailable node service cannot produce a numeric game latency.'
    Assert-True ($page.Text -notmatch '192.0.2.10') 'The previous relay must not appear as a fallback result.'
}
Test-Case 'a single resolved relay IPv4 is pinned for direct TCP and remote probe identity' {
    $page = Invoke-TestLatencyPage -Mode 'domain-single'
    Assert-Equal $page.Calls 'relay,remote' 'A uniquely resolved node should support both measurements.'
    Assert-Equal $page.RemoteTargets '192.0.2.20:20424 via 127.0.0.1:10088' 'The remote request must use the same concrete IPv4 as the direct relay TCP test.'
    Assert-True ($page.Text -match '中转测试目标：192.0.2.20:34069') 'The selected resolved endpoint should be visible.'
    Assert-True ($page.Text -match '(?m)^\s*当前到游戏服务器延迟：225 ms\s*$') 'The resolved relay must use its actual two-segment sum.'
    Assert-Equal $page.IpCount 1 'Exit IP must still use the active local proxy port once.'
}
Test-Case 'ambiguous IPv4 records IPv6-only records and DNS timeout never mix relay measurements' {
    foreach ($mode in @('domain-multiple', 'domain-ipv6', 'domain-timeout')) {
        $page = Invoke-TestLatencyPage -Mode $mode
        Assert-Equal $page.Calls '' 'An unresolvable unique relay must not initiate either TCP segment measurement.'
        Assert-Equal $page.RemoteTargets '' 'No old or arbitrary relay probe service may be used after a DNS failure.'
        Assert-Equal $page.IpCount 1 'Relay resolution failure must not suppress the SOCKS exit-IP lookup.'
        Assert-Equal $page.SocksPort 10088 'The active local SOCKS port remains valid even when relay DNS is unusable.'
        Assert-Equal $page.WaitCount 1 'The page must retain its return prompt.'
        Assert-True ($page.Text -match '当前出口 IPv4：198\.51\.100\.7') 'Independent exit-IP success must remain visible.'
        $latencyLines = @($page.Text -split '\r?\n' | Where-Object { $_ -match '当前到(中转|游戏)服务器延迟：' })
        Assert-Equal $latencyLines.Count 2 'Both unavailable latency results should be visible.'
        foreach ($line in $latencyLines) {
            Assert-True ($line -notmatch '\d+(?:\.\d+)?\s*ms') 'Unusable DNS cannot produce a numeric latency.'
            Assert-True ($line -match '解析|超时|DNS|IPv4|唯一|多个') ($mode + ' should supply a clear relay resolution error.')
        }
    }
}
Test-Case 'disabled service and unsupported local proxy configurations skip remote measurement' {
    foreach ($mode in @('disabled', 'authentication', 'invalid-port')) {
        $page = Invoke-TestLatencyPage -Mode $mode
        Assert-Equal $page.Calls 'relay' 'Unsupported service/proxy configurations must keep only the direct relay probe.'
        Assert-Equal $page.IpCount 1 'Exit IP remains independent.'
        Assert-Equal $page.WaitCount 1 'Return prompt'
        Assert-True ($page.Text -match '当前到中转服务器延迟：165 ms') 'Direct relay latency remains available.'
        $gameLine = @($page.Text -split '\r?\n' | Where-Object { $_ -match '当前到游戏服务器延迟：' })
        Assert-Equal $gameLine.Count 1 'Game status must remain visible.'
        Assert-True ($gameLine[0] -notmatch '\d+(?:\.\d+)?\s*ms') 'Unsupported configuration must not invent a numeric game latency.'
        $expectedReason = switch ($mode) {
            'disabled' { '未启用|未配置|未部署|不支持' }
            'authentication' { '认证' }
            'invalid-port' { '端口|SOCKS' }
        }
        Assert-True ($gameLine[0] -match $expectedReason) ($mode + ' needs a specific failure reason.')
    }
}
Test-Case 'missing effective config keeps fallback proxy IP lookup and menu return working' {
    $page = Invoke-TestLatencyPage -Mode 'missing'
    Assert-Equal $page.Calls '' 'Missing effective config must not trigger either segment measurement.'
    Assert-Equal $page.IpCount 1 'IP lookup remains independent of configuration parsing.'
    Assert-Equal $page.WaitCount 1 'Page must return through one existing Enter prompt.'
    Assert-Equal $page.SaveCount 1 'Fallback proxy port should update state once.'
    Assert-Equal $page.SocksPort 10087 'Fallback proxy port should synchronize to state.'
    Assert-True ($page.Text -match '当前出口 IPv4：198\.51\.100\.7') 'Successful fallback exit IP must remain visible.'
    Assert-True ($page.Text -match '当前到中转服务器延迟：.*无法唯一确定测试目标') 'Missing relay config needs its existing failure reason.'
    $gameLine = @($page.Text -split '\r?\n' | Where-Object { $_ -match '当前到游戏服务器延迟：' })
    Assert-Equal $gameLine.Count 1 'Game status must remain visible.'
    Assert-True ($gameLine[0] -notmatch '\d+(?:\.\d+)?\s*ms') 'Missing config must not produce a fabricated game timing.'
}

Write-Host ('{0} passed; {1} failed. PowerShell {2}' -f $script:Passed, $script:Failed, $PSVersionTable.PSVersion)
if ($script:Failed -gt 0) { exit 1 }
