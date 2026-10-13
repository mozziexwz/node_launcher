<#
.SYNOPSIS
  Regenerate the application and tray icons from build\icons\*.svg.

.DESCRIPTION
  Each size is rendered by headless Microsoft Edge at 16x (4x for 256) and
  box-filtered down by scripts\icotool, which writes the multi-size .ico files.

  Outputs:
    build\appicon.png        1024 px, full mark
    build\windows\icon.ico   16-40 px leaf only, 48-256 px full mark
    build\tray\idle.ico      leaf only, white
    build\tray\running.ico   leaf only, orange (shown while accelerating)

  Requires Microsoft Edge and Go.
#>
[CmdletBinding()]
param(
    [string]$Edge = (Join-Path ${env:ProgramFiles(x86)} 'Microsoft\Edge\Application\msedge.exe')
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$icons = Join-Path $root 'build\icons'
$work = Join-Path $root '.cache\icons'
New-Item -ItemType Directory -Force -Path $work | Out-Null
if (-not (Test-Path -LiteralPath $Edge)) { throw "Microsoft Edge not found: $Edge" }

function Render([string]$svg, [int]$px) {
    $name = '{0}-{1}' -f [IO.Path]::GetFileNameWithoutExtension($svg), $px
    $html = Join-Path $work "$name.html"
    $png = Join-Path $work "$name.png"
    $markup = [IO.File]::ReadAllText((Join-Path $icons $svg))
    $page = '<!doctype html><html><head><style>html,body{margin:0;background:transparent;overflow:hidden}' +
            "svg{display:block;width:${px}px;height:${px}px}</style></head><body>$markup</body></html>"
    [IO.File]::WriteAllText($html, $page)
    Remove-Item -LiteralPath $png -ErrorAction SilentlyContinue
    $p = Start-Process -FilePath $Edge -Wait -PassThru -WindowStyle Hidden -ArgumentList @(
        '--headless=new', '--disable-gpu', '--hide-scrollbars', '--no-first-run',
        "--user-data-dir=$work\edge-profile", '--force-device-scale-factor=1',
        '--default-background-color=00000000', "--window-size=$px,$px",
        "--screenshot=$png", ('file:///' + $html.Replace('\', '/')))
    if (-not (Test-Path -LiteralPath $png)) { throw "Edge did not render $name (exit $($p.ExitCode))" }
    return $png
}

$sizes = 16, 20, 24, 32, 40, 48, 64
$leaf = @{}
$running = @{}
foreach ($s in $sizes) {
    $leaf[$s] = Render 'leaf.svg' ($s * 16)
    $running[$s] = Render 'running.svg' ($s * 16)
}
$full768 = Render 'full.svg' 768
$full1024 = Render 'full.svg' 1024

$tool = Join-Path $work 'icotool.exe'
Push-Location $root
try {
    go build -o $tool ./scripts/icotool
    if ($LASTEXITCODE -ne 0) { throw 'go build icotool failed' }
} finally {
    Pop-Location
}

$app = @(16, 20, 24, 32, 40 | ForEach-Object { "$_=$($leaf[$_])" }) + @("48=$full768", "64=$full1024", "256=$full1024")
& $tool (Join-Path $root 'build\windows\icon.ico') @app
& $tool (Join-Path $root 'build\tray\idle.ico') @($sizes | ForEach-Object { "$_=$($leaf[$_])" })
& $tool (Join-Path $root 'build\tray\running.ico') @($sizes | ForEach-Object { "$_=$($running[$_])" })
if ($LASTEXITCODE -ne 0) { throw 'icotool failed' }
Copy-Item -LiteralPath $full1024 -Destination (Join-Path $root 'build\appicon.png') -Force
Write-Host 'Icons written to build\'
