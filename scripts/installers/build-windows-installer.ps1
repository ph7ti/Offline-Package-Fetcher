param(
    [ValidateSet('amd64','arm64')][string]$Arch = 'amd64',
    [string]$PayloadRoot = 'dist\installer-payload',
    [string]$Version = '0.4.1',
    [string]$OutDir = 'dist\installers',
    [ValidateSet('cli')][string]$Edition = 'cli'
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$payload = (Resolve-Path (Join-Path $repo $PayloadRoot)).Path
$out = Join-Path $repo $OutDir
New-Item -ItemType Directory -Force $out | Out-Null

$exe = Join-Path $out "opf-CLI-installer-windows-$Arch.exe"
Remove-Item $exe -Force -ErrorAction SilentlyContinue
Push-Location $repo
try {
    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    & go build -trimpath -ldflags "-s -w -buildid= -X main.version=$Version -X main.edition=cli" -o $exe ./cmd/opf-installer
    if ($LASTEXITCODE -ne 0) { throw "go build installer failed: $LASTEXITCODE" }
} finally {
    Pop-Location
}
if (-not (Test-Path $exe)) { throw "installer not found: $exe" }
$uninst = Join-Path $out "opf-uninstaller-windows-$Arch.exe"
Remove-Item $uninst -Force -ErrorAction SilentlyContinue
Push-Location $repo
try {
    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    & go build -trimpath -ldflags "-s -w -buildid=" -o $uninst ./cmd/opf-uninstaller
    if ($LASTEXITCODE -ne 0) { throw "go build uninstaller failed: $LASTEXITCODE" }
} finally {
    Pop-Location
}
Write-Host "built $exe ($((Get-Item $exe).Length) bytes)"
Write-Host "built $uninst ($((Get-Item $uninst).Length) bytes)"

$stage = Join-Path $out "windows-$Arch"
Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force (Join-Path $stage 'payload') | Out-Null
Copy-Item $exe (Join-Path $stage 'opf-CLI-installer.exe') -Force
foreach ($name in 'opf-cli.exe','opf-tui.exe') {
    $src = Join-Path $payload "$Arch\$name"
    if (-not (Test-Path $src)) { throw "missing payload binary: $src" }
    Copy-Item $src (Join-Path $stage "payload\$name") -Force
}
Copy-Item $uninst (Join-Path $stage 'payload\uninstall-opf.exe') -Force
$zip = Join-Path $out "opf-CLI-installer-windows-$Arch.zip"
Remove-Item $zip -Force -ErrorAction SilentlyContinue
Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -CompressionLevel Optimal
Write-Host "built $zip ($((Get-Item $zip).Length) bytes)"

if ($Arch -eq 'amd64' -and $env:OPF_TEST_INSTALLER -eq '1') {
    $target = Join-Path $env:TEMP "opf-installer-test-$Arch"
    Remove-Item $target -Recurse -Force -ErrorAction SilentlyContinue
    $p = Start-Process -FilePath (Join-Path $stage 'opf-CLI-installer.exe') -ArgumentList @('--silent', '--profile', 'cli', '--dir', $target) -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "installer test failed: $($p.ExitCode)" }
    if (-not (Test-Path (Join-Path $target 'opf-cli.exe'))) { throw "installer test did not produce opf-cli.exe" }
    if (-not (Test-Path (Join-Path $target 'uninstall-opf.exe'))) { throw "installer test did not produce uninstall-opf.exe" }
    $u = Start-Process -FilePath (Join-Path $target 'uninstall-opf.exe') -ArgumentList @('--silent', '--remove-data') -Wait -PassThru
    if ($u.ExitCode -ne 0) { throw "uninstaller test failed: $($u.ExitCode)" }
    for ($i = 0; $i -lt 30 -and (Test-Path $target); $i++) { Start-Sleep -Milliseconds 200 }
    if (Test-Path $target) { throw "uninstaller test left files in $target" }
    Write-Host "installer/uninstaller test OK: $target"
}
