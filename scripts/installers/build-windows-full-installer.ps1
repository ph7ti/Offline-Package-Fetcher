param(
    [ValidateSet('amd64','arm64')][string]$Arch = 'amd64',
    [string]$PayloadRoot = 'dist\installer-payload',
    [string]$Version = '0.2.8',
    [string]$OutDir = 'dist\installers'
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$iss = Join-Path $repo 'installers\windows\opf-full.iss'
$payload = (Resolve-Path (Join-Path $repo $PayloadRoot)).Path
$out = Join-Path $repo $OutDir
New-Item -ItemType Directory -Force $out | Out-Null

$isccCandidates = @(
    (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe'),
    (Join-Path $env:ProgramFiles 'Inno Setup 6\ISCC.exe'),
    (Get-Command ISCC.exe -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source)
) | Where-Object { $_ -and (Test-Path $_) }
if (-not $isccCandidates) { throw 'ISCC.exe not found; install Inno Setup 6' }
$iscc = @($isccCandidates)[0]

Write-Host "==> Inno Setup: $iscc"
& $iscc "/DMyArch=$Arch" "/DAppVersion=$Version" "/DPayloadRoot=$payload" "/O$out" $iss
if ($LASTEXITCODE -ne 0) { throw "ISCC failed with exit code $LASTEXITCODE" }

$exe = Join-Path $out "opf-Full-installer-windows-$Arch.exe"
if (-not (Test-Path $exe)) { throw "full installer not found: $exe" }
$zip = Join-Path $out "opf-Full-installer-windows-$Arch.zip"
Remove-Item $zip -Force -ErrorAction SilentlyContinue
Compress-Archive -Path $exe -DestinationPath $zip -CompressionLevel Optimal
Write-Host "built $exe ($((Get-Item $exe).Length) bytes)"
Write-Host "built $zip ($((Get-Item $zip).Length) bytes)"

if ($Arch -eq 'amd64' -and $env:OPF_TEST_INSTALLER -eq '1') {
    $target = Join-Path $env:TEMP "opf-full-installer-test-$Arch"
    Remove-Item $target -Recurse -Force -ErrorAction SilentlyContinue
    $p = Start-Process -FilePath $exe -ArgumentList @(
        '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART',
        '/TYPE=cli', "/DIR=$target"
    ) -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "full installer test failed: $($p.ExitCode)" }
    if (-not (Test-Path (Join-Path $target 'opf-cli.exe'))) {
        throw "full installer test did not produce opf-cli.exe"
    }
    $uninstall = Join-Path $target 'unins000.exe'
    if (Test-Path $uninstall) {
        $u = Start-Process -FilePath $uninstall -ArgumentList @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART') -Wait -PassThru
        if ($u.ExitCode -ne 0) { throw "full uninstaller test failed: $($u.ExitCode)" }
    }
    for ($i = 0; $i -lt 30 -and (Test-Path $target); $i++) { Start-Sleep -Milliseconds 200 }
    if (Test-Path $target) { throw "full uninstaller test left files in $target" }
    Write-Host "full installer/uninstaller test OK: $target"
}
