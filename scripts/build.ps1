<#
.SYNOPSIS
  Builds the Mayank 2.0 dashboard (web/dist) and the daemon (bin/mayank2.exe).

.DESCRIPTION
  1. web/: npm ci (only when node_modules is missing), then npm run build -> web/dist
  2. go build -ldflags "-H windowsgui" -o bin/mayank2.exe ./cmd/mayank2

  The exe is built with -H windowsgui so the logon task runs it without a
  console window. Use `go run ./cmd/mayank2 doctor` when you want console output.

.PARAMETER SkipWeb
  Skip the web build (Go-only rebuild).

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts\build.ps1
#>
[CmdletBinding()]
param(
    [switch]$SkipWeb
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot

function Assert-Command([string]$Name) {
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "build: '$Name' is not on PATH (see README prerequisites)"
    }
}

# Runs a native command with a fixed argument list in a directory and fails on a
# non-zero exit code.
function Invoke-Step([string]$Dir, [string]$Exe, [string[]]$Arguments) {
    Write-Host "==> $Exe $($Arguments -join ' ')   (in $Dir)"
    Push-Location $Dir
    try {
        & $Exe @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "build: '$Exe $($Arguments -join ' ')' failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
}

if (-not $SkipWeb) {
    Assert-Command 'npm'
    $web = Join-Path $repo 'web'
    if (-not (Test-Path (Join-Path $web 'node_modules'))) {
        Invoke-Step $web 'npm' @('ci', '--no-audit', '--no-fund')
    }
    Invoke-Step $web 'npm' @('run', 'build')
    if (-not (Test-Path (Join-Path $web 'dist\index.html'))) {
        throw 'build: web/dist/index.html was not produced'
    }
}

# A shell opened before Go was installed may not have it on PATH yet.
if (-not (Get-Command 'go' -ErrorAction SilentlyContinue)) {
    $goBin = Join-Path $env:ProgramFiles 'Go\bin'
    if (Test-Path (Join-Path $goBin 'go.exe')) { $env:Path = "$goBin;$env:Path" }
}
Assert-Command 'go'
$out = Join-Path $repo 'bin\mayank2.exe'
Invoke-Step $repo 'go' @('build', '-ldflags', '-H windowsgui', '-o', $out, './cmd/mayank2')

$size = [math]::Round((Get-Item $out).Length / 1MB, 1)
Write-Host "build: ok -> $out ($size MB)"
