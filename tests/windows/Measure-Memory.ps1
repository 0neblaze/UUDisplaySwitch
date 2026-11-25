param(
    [Parameter(Mandatory)][string]$Label,
    [Parameter(Mandatory)][string]$OutputPath,
    [ValidateRange(3,60)][int]$Samples = 10,
    [ValidateRange(200,10000)][int]$IntervalMs = 2000,
    [int[]]$ProcessIds = @(),
    [switch]$RequireWindowProcessAbsent
)
$ErrorActionPreference = 'Stop'

if ($RequireWindowProcessAbsent -and -not ('SettingsWindowProbe' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public static class SettingsWindowProbe {
    public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);
    [DllImport("user32.dll")]
    public static extern bool EnumWindows(EnumWindowsProc lpEnumFunc, IntPtr lParam);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    public static extern int GetClassNameW(IntPtr hWnd, StringBuilder lpClassName, int nMaxCount);
    [DllImport("user32.dll")]
    public static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint lpdwProcessId);

    public static bool HasSettingsWindow(int[] targetPids) {
        bool found = false;
        var pidSet = targetPids != null && targetPids.Length > 0 ? new HashSet<int>(targetPids) : null;
        EnumWindows((hWnd, lParam) => {
            var sb = new StringBuilder(256);
            int len = GetClassNameW(hWnd, sb, sb.Capacity);
            if (len <= 0) return true;
            string cls = sb.ToString();
            if (!cls.StartsWith("UUDisplaySwitch.Settings.Window.v1", StringComparison.Ordinal)) return true;
            if (pidSet == null) {
                found = true;
                return false;
            }
            uint pid = 0;
            GetWindowThreadProcessId(hWnd, out pid);
            if (pidSet.Contains((int)pid)) {
                found = true;
                return false;
            }
            return true;
        }, IntPtr.Zero);
        return found;
    }
}
'@
}

if ($ProcessIds.Count -eq 0) {
    $legacyCore = @(Get-Process -Name 'UUDisplayCore' -ErrorAction SilentlyContinue)
    if ($legacyCore.Count -gt 0) {
        $legacyPids = ($legacyCore | ForEach-Object { $_.Id }) -join ', '
        throw "Legacy UUDisplayCore process is still running (PID: $legacyPids); stop it before measuring Route C1 baseline."
    }
    $legacyWinUI = @()
    foreach ($proc in @(Get-Process -Name 'UUDisplaySwitch' -ErrorAction SilentlyContinue)) {
        $isLegacy = $false
        try {
            $exeDir = [IO.Path]::GetDirectoryName($proc.MainModule.FileName)
            if ($exeDir -and (Test-Path -LiteralPath (Join-Path $exeDir 'UUDisplayCore.exe'))) {
                $isLegacy = $true
            }
            if (-not $isLegacy) {
                foreach ($mod in $proc.Modules) {
                    if ($mod.ModuleName -match '^(coreclr|hostfxr|Microsoft\.UI\.Xaml)\.dll$') {
                        $isLegacy = $true
                        break
                    }
                }
            }
        } catch {
        }
        if ($isLegacy) { $legacyWinUI += $proc.Id }
    }
    if ($legacyWinUI.Count -gt 0) {
        throw "Legacy WinUI UUDisplaySwitch process is still running (PID: $($legacyWinUI -join ', ')); stop it before measuring Route C1 baseline."
    }
}

function Get-CpuSecondsTotal([int[]]$Ids) {
    $total = 0.0
    foreach ($procId in $Ids) {
        $p = Get-Process -Id $procId -ErrorAction SilentlyContinue
        if ($p) { $total += $p.TotalProcessorTime.TotalSeconds }
    }
    return $total
}

$measurements = @()
$initialPids = @()
$cpuStartSec = 0.0
$cpuEndSec = 0.0
$stopwatch = $null
for ($sample = 0; $sample -lt $Samples; $sample++) {
    $processes = @(Get-CimInstance Win32_PerfFormattedData_PerfProc_Process -Filter "Name LIKE 'UUDisplaySwitch%'" |
        Where-Object { $_.Name -match '^UUDisplaySwitch(#\d+)?$' } |
        Where-Object { $ProcessIds.Count -eq 0 -or ($ProcessIds -contains [int]$_.IDProcess) } |
        Select-Object Name, IDProcess, WorkingSetPrivate, WorkingSet, PrivateBytes, HandleCount)

    if ($processes.Count -eq 0) { throw 'No matching UUDisplaySwitch processes were found.' }

    $pids = @($processes | ForEach-Object { [int]$_.IDProcess })
    if ($RequireWindowProcessAbsent) {
        $checkPids = if ($ProcessIds.Count -gt 0) { $ProcessIds } else { $pids }
        if ([SettingsWindowProbe]::HasSettingsWindow($checkPids)) {
            throw 'The settings window is still open when RequireWindowProcessAbsent was requested.'
        }
    }

    if ($sample -eq 0) {
        $initialPids = $pids
        $cpuStartSec = Get-CpuSecondsTotal $initialPids
        $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    }
    if ($sample -eq ($Samples - 1)) {
        $elapsedSec = $stopwatch.Elapsed.TotalSeconds
        $cpuEndSec = Get-CpuSecondsTotal $initialPids
    }

    $measurements += [pscustomobject]@{
        at = [DateTimeOffset]::Now.ToString('o')
        processes = $processes
        privateWorkingSet = [long](($processes | Measure-Object WorkingSetPrivate -Sum).Sum)
        workingSet = [long](($processes | Measure-Object WorkingSet -Sum).Sum)
        privateBytes = [long](($processes | Measure-Object PrivateBytes -Sum).Sum)
        handleCount = [long](($processes | Measure-Object HandleCount -Sum).Sum)
    }
    if ($sample -lt $Samples - 1) { Start-Sleep -Milliseconds $IntervalMs }
}

function Median($values) {
    $ordered = @($values | Sort-Object)
    return $ordered[[int][Math]::Floor($ordered.Count / 2)]
}

$cpuPercent = 0.0
if ($elapsedSec -gt 0) {
    $cpuDelta = [Math]::Max(0.0, $cpuEndSec - $cpuStartSec)
    $cpuPercent = [Math]::Round(($cpuDelta / $elapsedSec) * 100.0, 4)
}

$report = [pscustomobject]@{
    label = $Label
    sampleCount = $Samples
    privateWorkingSetMedian = Median $measurements.privateWorkingSet
    workingSetMedian = Median $measurements.workingSet
    privateBytesMedian = Median $measurements.privateBytes
    handleCountMedian = Median $measurements.handleCount
    cpuPercent = $cpuPercent
    elapsedSeconds = [Math]::Round($elapsedSec, 3)
    measurements = $measurements
}
$OutputPath = [IO.Path]::GetFullPath($OutputPath)
[IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($OutputPath)) | Out-Null
$report | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $OutputPath -Encoding utf8
[pscustomobject]@{
    label = $Label
    privateWorkingSetMiB = [Math]::Round($report.privateWorkingSetMedian / 1MB, 2)
    workingSetMiB = [Math]::Round($report.workingSetMedian / 1MB, 2)
    privateBytesMiB = [Math]::Round($report.privateBytesMedian / 1MB, 2)
    handleCountMedian = $report.handleCountMedian
    cpuPercent = $report.cpuPercent
    report = $OutputPath
} | ConvertTo-Json -Compress
