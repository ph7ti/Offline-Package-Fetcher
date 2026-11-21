<#
.SYNOPSIS
    Reproducible OPF security scan (Windows).

.DESCRIPTION
    Runs the security regression suite plus the scanners used to produce
    docs/SECURITY.md. The script is fail-fast: the first failing gate aborts
    with a non-zero exit code. Reports are written to bin/security/ (ignored by
    git).

    Gates (in order):
      1. go test -race -count=1 ./test/security/...   (security suite, race)
      2. go test -count=1 ./...                      (no regression)
      3. go vet ./...
      4. gitleaks dir .                              (working tree, no history)
      5. govulncheck ./...                           (reachable vulnerabilities)
      6. gosec -severity high -confidence high ./... (CI gate)
      7. gosec -severity medium -confidence medium   (inventory, non-gating)

    gosec medium/medium output is reported but does not fail the scan: those
    findings are triaged in docs/SECURITY.md. The high/high gate is the same
    command used by .github/workflows/ci.yml and must exit 0.

.PARAMETER NoRace
    Skip the -race run (useful where CGO/gcc is unavailable).

.EXAMPLE
    pwsh -File scripts/security/scan.ps1

.EXAMPLE
    pwsh -File scripts/security/scan.ps1 -NoRace
#>
[CmdletBinding()]
param(
    [switch]$NoRace
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $repoRoot

$reportDir = Join-Path $repoRoot 'bin\security'
New-Item -ItemType Directory -Force -Path $reportDir | Out-Null

function Resolve-Tool {
    param([string]$Name, [string[]]$Fallbacks = @())
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    foreach ($candidate in $Fallbacks) {
        if (Test-Path $candidate) { return $candidate }
    }
    throw "required tool '$Name' not found on PATH (install it or add it to C:\tools\secbin)"
}

function Invoke-Gate {
    param([string]$Name, [scriptblock]$Action)
    Write-Host ''
    Write-Host "==> $Name" -ForegroundColor Cyan
    & $Action
    if ($LASTEXITCODE -ne 0) {
        throw "GATE FAILED: $Name (exit code $LASTEXITCODE)"
    }
}

$go = Resolve-Tool 'go' @('C:\tools\go1.27.4\bin\go.exe')
$gitleaks = Resolve-Tool 'gitleaks' @('C:\tools\secbin\gitleaks.exe', (Join-Path $env:USERPROFILE 'go\bin\gitleaks.exe'))
$govulncheck = Resolve-Tool 'govulncheck' @('C:\tools\secbin\govulncheck.exe', (Join-Path $env:USERPROFILE 'go\bin\govulncheck.exe'))
$gosec = Resolve-Tool 'gosec' @('C:\tools\secbin\gosec.exe', (Join-Path $env:USERPROFILE 'go\bin\gosec.exe'))
$staticcheck = $null
try { $staticcheck = Resolve-Tool 'staticcheck' @('C:\tools\secbin\staticcheck.exe', (Join-Path $env:USERPROFILE 'go\bin\staticcheck.exe')) } catch { }

if (-not $env:CGO_ENABLED) { $env:CGO_ENABLED = '1' }

Write-Host "OPF security scan - $repoRoot"
Write-Host "date: $(Get-Date -Format o)"
Write-Host ''
Write-Host '--- tool versions ---'
& $go version
Write-Host "gitleaks:   $(& $gitleaks version 2>&1)"
Write-Host "govulncheck:$(& $govulncheck -version 2>&1 | Select-Object -First 1)"
Write-Host "gosec:      $(& $gosec -version 2>&1 | Select-Object -First 1)"
if ($staticcheck) {
    Write-Host "staticcheck:$(& $staticcheck -version 2>&1)"
} else {
    Write-Host 'staticcheck: not installed (optional)'
}
Write-Host "CGO_ENABLED=$env:CGO_ENABLED"

if ($NoRace) {
    Invoke-Gate 'go test ./test/security/...' { & $go test -count=1 ./test/security/... }
} else {
    Invoke-Gate 'go test -race ./test/security/...' { & $go test -race -count=1 ./test/security/... }
}
Invoke-Gate 'go test ./...' { & $go test -count=1 ./... }
Invoke-Gate 'go vet ./...' { & $go vet ./... }

Invoke-Gate 'gitleaks dir .' {
    & $gitleaks dir . --no-banner --redact --report-format json --report-path (Join-Path $reportDir 'gitleaks.json')
}

Invoke-Gate 'govulncheck ./...' {
    & $govulncheck ./... 2>&1 | Tee-Object -FilePath (Join-Path $reportDir 'govulncheck.txt')
}

Invoke-Gate 'gosec -severity high -confidence high ./...' {
    & $gosec -severity high -confidence high ./...
}

Write-Host ''
Write-Host '==> gosec -severity medium -confidence medium (inventory, non-gating)' -ForegroundColor Cyan
& $gosec -severity medium -confidence medium -fmt json -out (Join-Path $reportDir 'gosec-medium.json') ./... | Out-Null
$mediumExit = $LASTEXITCODE
$mediumReport = Join-Path $reportDir 'gosec-medium.json'
if (Test-Path $mediumReport) {
    try {
        $parsed = Get-Content $mediumReport -Raw | ConvertFrom-Json
        Write-Host "gosec medium/medium findings: $($parsed.Issues.Count) (triaged in docs/SECURITY.md)"
    } catch {
        Write-Host "gosec medium/medium: could not parse report ($($_.Exception.Message))"
    }
} else {
    Write-Host "gosec medium/medium: no report written (exit $mediumExit)"
}

if ($staticcheck) {
    Write-Host ''
    Write-Host '==> staticcheck ./... (informational)' -ForegroundColor Cyan
    & $staticcheck ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "staticcheck reported findings (exit $LASTEXITCODE); triaged in docs/SECURITY.md"
    }
}

Write-Host ''
Write-Host 'ALL SECURITY GATES PASSED' -ForegroundColor Green
Write-Host "reports: $reportDir"
