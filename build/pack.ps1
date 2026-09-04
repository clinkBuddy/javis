# Builds a single-file Windows installer with NSIS: bin\JARVIS-Setup-<version>.exe
#
# The installer copies jarvis.exe into Program Files, registers the Windows
# service, and creates a desktop shortcut named JARVIS.
[CmdletBinding()]
param(
    [string]$Version = "0.1.0",
    [switch]$Web
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Find-Makensis {
    $candidates = @(
        (Join-Path ${env:ProgramFiles} "NSIS\makensis.exe"),
        (Join-Path ${env:ProgramFiles(x86)} "NSIS\makensis.exe"),
        (Join-Path $root ".tools\nsis\makensis.exe")
    )
    $cmd = Get-Command makensis -ErrorAction SilentlyContinue
    if ($cmd) {
        $candidates = @($cmd.Source) + $candidates
    }

    foreach ($p in $candidates) {
        if ($p -and (Test-Path $p)) {
            return $p
        }
    }

    Write-Host "NSIS not found; downloading portable makensis" -ForegroundColor Yellow
    return Install-PortableNsis
}

function Install-PortableNsis {
    $ver = "3.11"
    $zipName = "nsis-$ver.zip"
    $tools = Join-Path $root ".tools"
    $dest = Join-Path $tools "nsis"
    $zip = Join-Path $tools $zipName
    New-Item -ItemType Directory -Force -Path $tools | Out-Null

    $url = "https://downloads.sourceforge.net/project/nsis/NSIS%203/$ver/$zipName"
    Write-Host "downloading $url" -ForegroundColor Cyan
    Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing -UserAgent "JARVIS-pack/1.0"
    if (-not (Test-Path $zip) -or (Get-Item $zip).Length -lt 10000) {
        throw "NSIS download failed. Install NSIS from https://nsis.sourceforge.io/Download and re-run pack.ps1"
    }

    if (Test-Path $dest) {
        Remove-Item -Recurse -Force $dest
    }
    Expand-Archive -Path $zip -DestinationPath $tools -Force
    $extracted = Join-Path $tools "nsis-$ver"
    if (Test-Path $extracted) {
        Rename-Item -Path $extracted -NewName "nsis"
    }
    $exe = Join-Path $dest "makensis.exe"
    if (-not (Test-Path $exe)) {
        throw "makensis.exe missing after extracting NSIS. Install NSIS from https://nsis.sourceforge.io/Download"
    }
    return $exe
}

$buildArgs = @{
    Version = $Version
    Release = $true
    Output  = "bin\jarvis.exe"
}
if ($Web) { $buildArgs.Web = $true }

Write-Host "building product" -ForegroundColor Cyan
& (Join-Path $PSScriptRoot "build.ps1") @buildArgs
if ($LASTEXITCODE -ne 0) { throw "product build failed" }

$stage = Join-Path $root "bin\nsis-payload"
New-Item -ItemType Directory -Force -Path $stage | Out-Null
Copy-Item -Force (Join-Path $root "bin\jarvis.exe") (Join-Path $stage "jarvis.exe")

$ico = Join-Path $stage "jarvis.ico"
Write-Host "writing icon $ico" -ForegroundColor Cyan
go run .\build\nsis\genico.go $ico
if ($LASTEXITCODE -ne 0) { throw "icon generation failed" }

$makensis = Find-Makensis
$out = Join-Path $root "bin\JARVIS-Setup-$Version.exe"
Write-Host "building installer $out" -ForegroundColor Cyan
& $makensis `
    "/INPUTCHARSET" "UTF8" `
    "/DVERSION=$Version" `
    "/DPAYLOAD=$stage" `
    "/DOUTFILE=$out" `
    (Join-Path $PSScriptRoot "nsis\jarvis.nsi")
if ($LASTEXITCODE -ne 0) { throw "makensis failed" }

$size = [math]::Round((Get-Item $out).Length / 1MB, 1)
Write-Host "built $out ($size MB)" -ForegroundColor Green
