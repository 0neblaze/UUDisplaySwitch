param([string]$Destination = (Join-Path $PSScriptRoot 'dist\UUDisplaySwitch'))
$ErrorActionPreference = 'Stop'
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) { $go = Join-Path $env:ProgramFiles 'Go\bin\go.exe' }
$dotnet = (Get-Command dotnet -ErrorAction SilentlyContinue).Source
if (-not $dotnet) { $dotnet = Join-Path $env:ProgramFiles 'dotnet\dotnet.exe' }
if (-not (Test-Path -LiteralPath $go) -or -not (Test-Path -LiteralPath $dotnet)) { throw 'Go and .NET SDK are required.' }
$Destination = [IO.Path]::GetFullPath($Destination)
New-Item -ItemType Directory -Path $Destination -Force | Out-Null
Push-Location $PSScriptRoot
try {
    & $go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    & $go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go analysis failed.' }
    & $go build -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $Destination 'UUDisplayCore.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    & $dotnet publish native\UUDisplaySwitch.csproj -c Release -p:Platform=x64 -o $Destination -v minimal
    if ($LASTEXITCODE -ne 0) { throw 'WinUI publish failed.' }
    foreach ($name in @('UUDisplayCore.exe','UUDisplaySwitch.exe','UUDisplaySwitch.pri','App.xbf','app.ico')) {
        if (-not (Test-Path -LiteralPath (Join-Path $Destination $name))) { throw "Missing published resource: $name" }
    }
    Write-Output "Published: $Destination"
} finally { Pop-Location }
