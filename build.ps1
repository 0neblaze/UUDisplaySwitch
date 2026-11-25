param([string]$Destination = (Join-Path $PSScriptRoot 'dist\UUDisplaySwitch'))
$ErrorActionPreference = 'Stop'

$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) { $go = Join-Path $env:ProgramFiles 'Go\bin\go.exe' }
if (-not (Test-Path -LiteralPath $go)) { throw 'Go toolchain is required.' }

$Destination = [IO.Path]::GetFullPath($Destination)
$sysoPath = Join-Path $PSScriptRoot 'rsrc_windows_amd64.syso'
$hadCgoEnv = Test-Path Env:CGO_ENABLED
$prevCgoEnabled = $env:CGO_ENABLED
Push-Location $PSScriptRoot
try {
    $env:CGO_ENABLED = '0'

    & $go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }

    & $go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet analysis failed.' }

    if (Test-Path -LiteralPath $Destination) {
        Remove-Item -LiteralPath $Destination -Recurse -Force
    }
    New-Item -ItemType Directory -Path $Destination -Force | Out-Null

    & $go run ./internal/genrsrc -ico app.ico -manifest app.manifest -o $sysoPath
    if ($LASTEXITCODE -ne 0) { throw 'PE resource (.syso) generation failed.' }

    $outExe = Join-Path $Destination 'UUDisplaySwitch.exe'
    & $go build -trimpath -ldflags '-s -w -H windowsgui' -o $outExe .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }

    if (-not (Test-Path -LiteralPath $outExe)) {
        throw "Missing published executable: $outExe"
    }
    Write-Output "Published: $outExe"
} finally {
    if ($hadCgoEnv) {
        $env:CGO_ENABLED = $prevCgoEnabled
    } else {
        Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    }
    if (Test-Path -LiteralPath $sysoPath) {
        Remove-Item -LiteralPath $sysoPath -Force -ErrorAction SilentlyContinue
    }
    Pop-Location
}
