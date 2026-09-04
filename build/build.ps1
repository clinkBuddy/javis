# Builds bin\jarvis.exe with version metadata stamped in.
#
# The binary is intentionally linked as a console application. Using
# -H=windowsgui would make the CLI subcommands silent, so the modes that should
# not show a window hide the console themselves at startup instead.
[CmdletBinding()]
param(
    [string]$Version = "0.1.0",
    [string]$Output = "bin\jarvis.exe",
    [switch]$Release,

    # The built UI is committed under internal/webui/dist so that `go build`
    # works on a machine without Node. Pass this to rebuild it from web/.
    [switch]$Web
)

$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

if ($Web) {
    Write-Host "building admin UI" -ForegroundColor Cyan
    Push-Location web
    try {
        if (-not (Test-Path node_modules)) {
            npm install --no-audit --no-fund
            if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
        }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
    } finally {
        Pop-Location
    }
}

$commit = "unknown"
try {
    $commit = (git rev-parse --short HEAD 2>$null).Trim()
    if ((git status --porcelain 2>$null)) { $commit += "-dirty" }
} catch {
    Write-Warning "git metadata unavailable; commit will be reported as 'unknown'"
}
$date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

$pkg = "github.com/sjkim/jarvis/internal/buildinfo"
$ldflags = "-X $pkg.Version=$Version -X $pkg.Commit=$commit -X $pkg.Date=$date"
if ($Release) {
    # Strip the symbol table and DWARF data. Roughly halves the binary.
    $ldflags += " -s -w"
}

# Pure Go: no C toolchain needed and the exe stays self-contained.
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

Write-Host "building $Output ($Version / $commit)" -ForegroundColor Cyan
go build -trimpath -ldflags $ldflags -o $Output .\cmd\jarvis
if ($LASTEXITCODE -ne 0) { throw "go build failed" }

$size = [math]::Round((Get-Item $Output).Length / 1MB, 1)
Write-Host "built $Output ($size MB)" -ForegroundColor Green
& ".\$Output" version
