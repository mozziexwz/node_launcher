$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$path = Join-Path $root 'MSBOOST启动器_全程TCP测速.bat'
$oldPath = Join-Path $root 'MSBOOST启动器.bat'
function Get-LauncherAst([string]$File) {
    $text = [IO.File]::ReadAllText($File, [Text.Encoding]::UTF8)
    $marker = '#<MSBOOST_PS>'
    $tokens = $null; $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseInput($text.Substring($text.IndexOf($marker) + $marker.Length), [ref]$tokens, [ref]$errors)
    if (@($errors).Count) { throw ($errors | Out-String) }
    return $ast
}
$ast = Get-LauncherAst $path
$oldAst = Get-LauncherAst $oldPath
$defs = @($ast.EndBlock.Statements | Where-Object { $_ -is [Management.Automation.Language.FunctionDefinitionAst] })
$oldDefs = @($oldAst.EndBlock.Statements | Where-Object { $_ -is [Management.Automation.Language.FunctionDefinitionAst] })
foreach ($name in @('Start-Boost','Stop-Boost','Select-AndReplaceConfig','Load-State','Save-State','Invoke-ExitIpLookup','Wait-ForMenu','Confirm-Exit','Show-MainMenu')) {
    $before = @($oldDefs | Where-Object Name -eq $name)[0].Extent.Text
    $after = @($defs | Where-Object Name -eq $name)[0].Extent.Text
    if ($null -eq $before -or $before -cne $after) { throw ('Original workflow changed: ' + $name) }
}
$oldHash = (Get-FileHash -LiteralPath $oldPath -Algorithm SHA256).Hash
if ($oldHash -cne 'E9408EF0D773E0192CFC71DD51F64C250567E35FF2EF97BC4853A0A0B63E0CCB') { throw 'Original BAT changed during delivery.' }
$bytes = [IO.File]::ReadAllBytes($path)
if ($bytes[0] -eq 239 -and $bytes[1] -eq 187 -and $bytes[2] -eq 191) { throw 'BAT must not have a UTF-8 BOM.' }
$text = [Text.Encoding]::UTF8.GetString($bytes)
if ($text.Replace("`r`n",'').Contains("`n")) { throw 'BAT must use CRLF.' }
if ($text -match 'Invoke-RelayTcpSamples|Merge-TcpLatencySamples|Read-GameHandshake|WaitForGameHandshake') { throw 'Obsolete game measurement survived.' }
'PASS: Windows PowerShell parser, original workflow preservation, original hash, encoding and no legacy game fallback.'
'PowerShell ' + $PSVersionTable.PSVersion
