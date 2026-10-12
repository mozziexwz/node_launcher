# Run with Windows PowerShell 5.1. This suite uses memory streams only.
param([string]$LauncherPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'MSBOOST启动器_全程TCP测速.bat'))

$ErrorActionPreference = 'Stop'
$script:Passed = 0
$script:Failed = 0
$script:Utf8 = New-Object Text.UTF8Encoding($false, $true)
$source = [IO.File]::ReadAllText($LauncherPath, $script:Utf8)
$marker = '#<MSBOOST_PS>'
$offset = $source.IndexOf($marker, [StringComparison]::Ordinal)
if ($offset -lt 0) { throw 'Embedded PowerShell marker is missing.' }
$tokens = $null; $parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseInput($source.Substring($offset + $marker.Length), [ref]$tokens, [ref]$parseErrors)
if (@($parseErrors).Count -ne 0) { throw ($parseErrors | Out-String) }
$definitions = @($ast.EndBlock.Statements | Where-Object { $_ -is [Management.Automation.Language.FunctionDefinitionAst] } | ForEach-Object { $_.Extent.Text })
. ([ScriptBlock]::Create([string]::Join([Environment]::NewLine, $definitions)))

if (-not ('FullPathBoundaries.ScriptedStream' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.IO;

namespace FullPathBoundaries {
    public sealed class ScriptedStream : Stream {
        private readonly MemoryStream source;
        private readonly int maxChunk;
        private int readTimeout;
        public int ReadCount { get; private set; }
        public bool Disposed { get; private set; }
        public ScriptedStream(byte[] data, int maximumChunk) {
            source = new MemoryStream(data, false);
            maxChunk = maximumChunk;
        }
        public override bool CanRead { get { return true; } }
        public override bool CanSeek { get { return false; } }
        public override bool CanWrite { get { return false; } }
        public override bool CanTimeout { get { return true; } }
        public override long Length { get { return source.Length; } }
        public override long Position { get { return source.Position; } set { throw new NotSupportedException(); } }
        public override int ReadTimeout { get { return readTimeout; } set { readTimeout = value; } }
        public override int Read(byte[] buffer, int offset, int count) {
            ReadCount++;
            return source.Read(buffer, offset, Math.Min(count, maxChunk));
        }
        public override void Flush() {}
        public override long Seek(long offset, SeekOrigin origin) { throw new NotSupportedException(); }
        public override void SetLength(long value) { throw new NotSupportedException(); }
        public override void Write(byte[] buffer, int offset, int count) { throw new NotSupportedException(); }
        protected override void Dispose(bool disposing) {
            if (disposing) { source.Dispose(); Disposed = true; }
            base.Dispose(disposing);
        }
    }
}
'@
}

$script:Nonce = '0123456789abcdef0123456789abcdef'
$script:NextNonce = 'abcdef0123456789abcdef0123456789'
$script:NodeId = '89abcdef0123456789abcdef01234567'
$script:Hello = '{"version":2,"operation":"hello","nonce":"' + $script:Nonce + '","node_id":"' + $script:NodeId + '","success":true}'
$script:Connect = '{"version":2,"operation":"connect","nonce":"' + $script:Nonce + '","node_id":"' + $script:NodeId + '","success":true,"target_address":"35.155.204.207","target_port":8585}'

function Assert-Equal($Actual, $Expected, [string]$Message) {
    if ($Actual -cne $Expected) { throw ($Message + ': expected [' + $Expected + '], received [' + $Actual + ']') }
}
function Assert-Rejected([scriptblock]$Action, [string]$Pattern = '') {
    $failure = $null
    try { & $Action | Out-Null } catch { $failure = $_ }
    if ($null -eq $failure) { throw 'Expected rejection, but the malformed input was accepted.' }
    if ($Pattern -and $failure.Exception.Message -notmatch $Pattern) {
        throw ('Unexpected rejection reason: ' + $failure.Exception.Message)
    }
}
function Test-Case([string]$Name, [scriptblock]$Action) {
    try { & $Action; $script:Passed++; Write-Host ('PASS ' + $Name) }
    catch { $script:Failed++; Write-Host ('FAIL ' + $Name + ': ' + $_.Exception.Message) }
}
function Read-TestFrame([byte[]]$Bytes, [int]$Chunk = 512) {
    $stream = New-Object FullPathBoundaries.ScriptedStream -ArgumentList $Bytes, $Chunk
    $watch = [Diagnostics.Stopwatch]::StartNew()
    try { return (Read-FullPathLine $stream $watch 2000) }
    finally { $watch.Stop(); $stream.Dispose() }
}
function Confirm-Hello([byte[]]$Bytes) {
    $reply = ConvertFrom-FullPathJson $Bytes
    Assert-FullPathReply -Reply $reply -Operation 'hello' -Nonce $script:Nonce
    return $reply
}
function Confirm-Connect([string]$Json, [string]$Nonce = $script:Nonce) {
    $reply = ConvertFrom-FullPathJson ($script:Utf8.GetBytes($Json))
    Assert-FullPathReply -Reply $reply -Operation 'connect' -Nonce $Nonce -NodeId $script:NodeId -GameAddress '35.155.204.207' -GamePort 8585
    return $reply
}

Test-Case 'LF frame validates and has a finite nonnegative client timestamp' {
    $line = Read-TestFrame ($script:Utf8.GetBytes($script:Hello + "`n"))
    $reply = Confirm-Hello $line.Bytes
    Assert-Equal $reply.success $true 'Hello success'
    if ([double]::IsNaN($line.Milliseconds) -or [double]::IsInfinity($line.Milliseconds) -or $line.Milliseconds -lt 0 -or $line.Milliseconds -gt 2000) { throw 'Invalid arrival timestamp.' }
}
Test-Case 'CRLF preserves trailing CR as permitted JSON whitespace' {
    $line = Read-TestFrame ($script:Utf8.GetBytes($script:Hello + "`r`n"))
    Assert-Equal $line.Bytes[$line.Bytes.Length - 1] 13 'CR remains inside the line'
    [void](Confirm-Hello $line.Bytes)
}
Test-Case 'one-byte fragmented JSON and LF are fully assembled' {
    $line = Read-TestFrame ($script:Utf8.GetBytes($script:Hello + "`n")) 1
    Assert-Equal ($script:Utf8.GetString($line.Bytes)) $script:Hello 'Reassembled response'
    [void](Confirm-Hello $line.Bytes)
}
Test-Case '4096 content bytes followed by LF are accepted' {
    $padded = $script:Hello + (' ' * (4096 - $script:Hello.Length))
    $line = Read-TestFrame ($script:Utf8.GetBytes($padded + "`n"))
    Assert-Equal $line.Bytes.Length 4096 'Maximum content length'
    [void](Confirm-Hello $line.Bytes)
}
Test-Case '4096 content bytes including CR followed by LF are accepted' {
    $padded = $script:Hello + (' ' * (4095 - $script:Hello.Length)) + "`r"
    $line = Read-TestFrame ($script:Utf8.GetBytes($padded + "`n")) 31
    Assert-Equal $line.Bytes.Length 4096 'CR counts in maximum content length'
    [void](Confirm-Hello $line.Bytes)
}
Test-Case '4097 content bytes before LF are rejected' {
    $padded = $script:Hello + (' ' * (4097 - $script:Hello.Length))
    Assert-Rejected { Read-TestFrame ($script:Utf8.GetBytes($padded + "`n")) } '过长'
}
Test-Case '4096 JSON bytes plus CR before LF exceed the limit' {
    $padded = $script:Hello + (' ' * (4096 - $script:Hello.Length))
    Assert-Rejected { Read-TestFrame ($script:Utf8.GetBytes($padded + "`r`n")) } '过长'
}
Test-Case 'EOF after a partial response is rejected' {
    Assert-Rejected { Read-TestFrame ($script:Utf8.GetBytes($script:Hello)) } '提前关闭'
}
Test-Case 'empty LF frame cannot validate as a hello reply' {
    $line = Read-TestFrame ([byte[]]@(10))
    Assert-Rejected { Confirm-Hello $line.Bytes }
}
Test-Case 'extra byte after LF in the same read is rejected' {
    Assert-Rejected { Read-TestFrame ($script:Utf8.GetBytes($script:Hello + "`nX")) } '额外数据'
}
Test-Case 'two complete merged reply lines in one read are rejected' {
    Assert-Rejected { Read-TestFrame ($script:Utf8.GetBytes($script:Hello + "`n" + $script:Hello + "`n")) } '额外数据'
}
Test-Case 'a separately arriving duplicate line remains visible and cannot match the next nonce' {
    $first = $script:Utf8.GetBytes($script:Connect + "`n")
    $both = [byte[]]($first + $first)
    $stream = New-Object FullPathBoundaries.ScriptedStream -ArgumentList $both, $first.Length
    $watch = [Diagnostics.Stopwatch]::StartNew()
    try {
        $line = Read-FullPathLine $stream $watch 2000
        [void](Confirm-Connect ($script:Utf8.GetString($line.Bytes)))
        Assert-Equal $stream.Position $first.Length 'Only the requested first frame was consumed'
        $line = Read-FullPathLine $stream $watch 2000
        Assert-Rejected { Confirm-Connect ($script:Utf8.GetString($line.Bytes)) $script:NextNonce } '随机标识'
        Assert-Equal $stream.Position $both.Length 'The retained duplicate frame was read rather than dropped'
    } finally { $watch.Stop(); $stream.Dispose() }
}
Test-Case 'JSON spacing outside strings accepts SP HT and CR' {
    $json = " `t`r" + $script:Hello.Replace(':', "`t:`r") + "`r`t "
    [void](Confirm-Hello ($script:Utf8.GetBytes($json)))
}

$invalidJsonCases = @(
    @{ Name = 'NUL outside strings'; Json = $script:Hello + [char]0 },
    @{ Name = 'TAB inside nonce string'; Json = $script:Hello.Replace($script:Nonce, $script:Nonce + "`t") },
    @{ Name = 'CR inside nonce string'; Json = $script:Hello.Replace($script:Nonce, $script:Nonce + "`r") },
    @{ Name = 'trailing non-whitespace'; Json = $script:Hello + 'x' },
    @{ Name = 'two JSON objects without a line boundary'; Json = $script:Hello + $script:Hello },
    @{ Name = 'nested node identity'; Json = $script:Hello.Replace('"' + $script:NodeId + '"', '{"id":"x"}') },
    @{ Name = 'array root'; Json = '[' + $script:Hello + ']' },
    @{ Name = 'null root'; Json = 'null' },
    @{ Name = 'duplicate nonce field'; Json = $script:Hello.Replace('"success":true', '"nonce":"' + $script:Nonce + '","success":true') },
    @{ Name = 'duplicate version field'; Json = $script:Hello.Replace('"version":2', '"version":2,"version":2') },
    @{ Name = 'uppercase field name'; Json = $script:Hello.Replace('"version"', '"Version"') },
    @{ Name = 'escaped unicode nonce'; Json = $script:Hello.Replace($script:Nonce, '\u0030' + $script:Nonce.Substring(1)) },
    @{ Name = 'leading-zero version'; Json = $script:Hello.Replace('"version":2', '"version":02') }
)
foreach ($case in $invalidJsonCases) {
    Test-Case ('reject malformed JSON: ' + $case.Name) { Assert-Rejected { Confirm-Hello ($script:Utf8.GetBytes($case.Json)) } }
}
Test-Case 'UTF-8 BOM is rejected' {
    $bytes = [byte[]](@(239, 187, 191) + $script:Utf8.GetBytes($script:Hello))
    Assert-Rejected { Confirm-Hello $bytes }
}
Test-Case 'malformed UTF-8 is rejected before JSON conversion' {
    Assert-Rejected { ConvertFrom-FullPathJson ([byte[]]@(123, 34, 195, 40, 34, 125)) }
}
Test-Case 'a physical LF inside a string ends an invalid frame' {
    $json = $script:Hello.Replace($script:Nonce, '0' + "`n" + $script:Nonce.Substring(1))
    Assert-Rejected {
        $line = Read-TestFrame ($script:Utf8.GetBytes($json + "`n")) 1
        Confirm-Hello $line.Bytes
    }
}

$invalidHelloCases = @(
    @{ Name = 'floating-point version'; Json = $script:Hello.Replace('"version":2', '"version":2.0') },
    @{ Name = 'exponent version'; Json = $script:Hello.Replace('"version":2', '"version":2e0') },
    @{ Name = 'boolean version'; Json = $script:Hello.Replace('"version":2', '"version":true') },
    @{ Name = 'string version'; Json = $script:Hello.Replace('"version":2', '"version":"2"') },
    @{ Name = 'old protocol version'; Json = $script:Hello.Replace('"version":2', '"version":1') },
    @{ Name = 'string success'; Json = $script:Hello.Replace('"success":true', '"success":"true"') },
    @{ Name = 'numeric success'; Json = $script:Hello.Replace('"success":true', '"success":1') },
    @{ Name = 'null success'; Json = $script:Hello.Replace('"success":true', '"success":null') },
    @{ Name = 'failed hello'; Json = $script:Hello.Replace('"success":true', '"success":false') },
    @{ Name = 'missing success'; Json = $script:Hello.Replace(',"success":true', '') },
    @{ Name = 'wrong operation'; Json = $script:Hello.Replace('"hello"', '"connect"') },
    @{ Name = 'wrong nonce'; Json = $script:Hello.Replace($script:Nonce, $script:NextNonce) },
    @{ Name = 'uppercase node identity'; Json = $script:Hello.Replace($script:NodeId, $script:NodeId.ToUpperInvariant()) },
    @{ Name = 'unknown compatibility timing'; Json = $script:Hello.Replace('"success":true', '"success":true,"connect_ms":1') },
    @{ Name = 'error on successful hello'; Json = $script:Hello.Replace('"success":true', '"success":true,"error":"failed"') }
)
foreach ($case in $invalidHelloCases) {
    Test-Case ('reject invalid hello reply: ' + $case.Name) { Assert-Rejected { Confirm-Hello ($script:Utf8.GetBytes($case.Json)) } }
}

Test-Case 'a complete matching connect success validates' {
    Assert-Equal (Confirm-Connect $script:Connect).success $true 'Connect result'
}
$invalidConnectCases = @(
    @{ Name = 'wrong game IPv4'; Json = $script:Connect.Replace('35.155.204.207', '51.222.56.192') },
    @{ Name = 'wrong game port'; Json = $script:Connect.Replace('8585', '8484') },
    @{ Name = 'floating-point game port'; Json = $script:Connect.Replace('8585', '8585.0') },
    @{ Name = 'exponent game port'; Json = $script:Connect.Replace('8585', '8585e0') },
    @{ Name = 'boolean game port'; Json = $script:Connect.Replace('8585', 'true') },
    @{ Name = 'string game port'; Json = $script:Connect.Replace('8585', '"8585"') },
    @{ Name = 'wrong protocol version'; Json = $script:Connect.Replace('"version":2', '"version":1') },
    @{ Name = 'wrong operation'; Json = $script:Connect.Replace('"connect"', '"hello"') },
    @{ Name = 'changed node identity'; Json = $script:Connect.Replace($script:NodeId, $script:NextNonce) },
    @{ Name = 'repeated prior nonce'; Json = $script:Connect.Replace($script:Nonce, $script:NextNonce) },
    @{ Name = 'error on successful connect'; Json = $script:Connect.Replace('"success":true', '"success":true,"error":"failed"') },
    @{ Name = 'missing error on failed connect'; Json = $script:Connect.Replace('"success":true', '"success":false') },
    @{ Name = 'unknown failure enum'; Json = $script:Connect.Replace('"success":true', '"success":false,"error":"unknown"') },
    @{ Name = 'legacy samples field'; Json = $script:Connect.Replace('"success":true', '"success":true,"samples":0') }
)
foreach ($case in $invalidConnectCases) {
    Test-Case ('reject invalid connect reply: ' + $case.Name) { Assert-Rejected { Confirm-Connect $case.Json } }
}
foreach ($reason in @('timeout', 'refused', 'unreachable', 'target_not_allowed', 'failed')) {
    Test-Case ('valid connect failure enum: ' + $reason) {
        $json = $script:Connect.Replace('"success":true', '"success":false,"error":"' + $reason + '"')
        $reply = Confirm-Connect $json
        Assert-Equal $reply.success $false 'Failed connect remains a failure'
        Assert-Equal $reply.error $reason 'Failure enum'
    }
}

Write-Host ('{0} passed; {1} failed. PowerShell {2}. Memory streams only; no sockets or network.' -f $script:Passed, $script:Failed, $PSVersionTable.PSVersion)
if ($script:Failed -gt 0) { exit 1 }
