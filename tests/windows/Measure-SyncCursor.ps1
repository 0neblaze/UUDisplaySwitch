param(
    [Parameter(Mandatory)][int]$AppId,
    [Parameter(Mandatory)][string]$ReportPath,
    [ValidateRange(6,60)][int]$Seconds = 15,
    [switch]$KeepPointerActive,
    [string]$ReferenceCore
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Threading;
using System.Threading.Tasks;
public static class CursorProbe {
    [StructLayout(LayoutKind.Sequential)] public struct Point { public int X,Y; }
    [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left,Top,Right,Bottom; }
    [StructLayout(LayoutKind.Sequential)] public struct Info { public int Size,Flags; public IntPtr Cursor; public Point Position; }
    [DllImport("user32.dll",SetLastError=true)] static extern bool GetCursorInfo(ref Info info);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern IntPtr LoadCursor(IntPtr instance,IntPtr name);
    [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
    [DllImport("user32.dll")] static extern void mouse_event(uint flags,uint dx,uint dy,uint data,UIntPtr extra);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window,out Rect rect);
    public sealed record Change(long Milliseconds,string Cursor,int Flags,int X,int Y);
    public sealed record Result(int Samples,int BusySamples,int OtherSamples,int VisibleSamples,int ReferenceQueries,Change[] Changes);
    public static Result Measure(int milliseconds,bool keepActive,int x,int y,string referenceCore) {
        var arrow=LoadCursor(IntPtr.Zero,(IntPtr)32512);
        var wait=LoadCursor(IntPtr.Zero,(IntPtr)32514);
        var starting=LoadCursor(IntPtr.Zero,(IntPtr)32650);
        var changes=new List<Change>();
        var referenceQueries=new List<Task>();
        var timer=Stopwatch.StartNew(); string previous=""; int samples=0,busy=0,other=0,visible=0; long nextMove=0,nextQuery=500;
        while(timer.ElapsedMilliseconds<milliseconds) {
            // Optional positive control reproduces the old launch, without changing device state.
            if(!string.IsNullOrEmpty(referenceCore)&&timer.ElapsedMilliseconds>=nextQuery){
                referenceQueries.Add(Task.Run(()=>{
                    using var child=Process.Start(new ProcessStartInfo(referenceCore,"--status"){UseShellExecute=false,CreateNoWindow=true,RedirectStandardOutput=true,RedirectStandardError=true});
                    var stdout=child.StandardOutput.ReadToEndAsync();var stderr=child.StandardError.ReadToEndAsync();
                    if(!child.WaitForExit(12000)){child.Kill();throw new Exception("Reference query timed out");}
                    Task.WaitAll(stdout,stderr);if(child.ExitCode!=0)throw new Exception("Reference query failed: "+stderr.Result);
                }));nextQuery+=3000;
            }
            if(keepActive&&timer.ElapsedMilliseconds>=nextMove){SetCursorPos(x,y);mouse_event(1,0,0,0,UIntPtr.Zero);nextMove=timer.ElapsedMilliseconds+100;}
            var info=new Info {Size=Marshal.SizeOf<Info>()};
            if(!GetCursorInfo(ref info)) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
            string kind=info.Cursor==arrow?"arrow":info.Cursor==wait?"wait":info.Cursor==starting?"app-starting":"other:"+info.Cursor;
            samples++; if((info.Flags&1)!=0)visible++; if(kind=="wait"||kind=="app-starting")busy++; else if(kind!="arrow")other++;
            if(kind+":"+info.Flags!=previous) {changes.Add(new Change(timer.ElapsedMilliseconds,kind,info.Flags,info.Position.X,info.Position.Y));previous=kind+":"+info.Flags;}
            Thread.Sleep(5);
        }
        Task.WaitAll(referenceQueries.ToArray());
        return new Result(samples,busy,other,visible,referenceQueries.Count,changes.ToArray());
    }
}
'@
$app = Get-Process -Id $AppId
if ($app.ProcessName -ne 'UUDisplaySwitch' -or $app.MainWindowHandle -eq 0) { throw 'A visible UUDisplaySwitch window is required.' }
if (-not (New-Object -ComObject WScript.Shell).AppActivate($AppId)) { throw 'Cannot activate the application for the cursor measurement.' }
Start-Sleep -Milliseconds 250
$window = [Windows.Automation.AutomationElement]::FromHandle($app.MainWindowHandle)
function Read-StatusText {
    $items = $window.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)
    @($items | Where-Object { $_.Current.AutomationId -eq 'device-status' -or $_.Current.Name -match '^(上次同步|Last synced|上次同步)' } | ForEach-Object { [pscustomobject]@{id=$_.Current.AutomationId;text=$_.Current.Name} })
}
$before = Read-StatusText
$rect = New-Object CursorProbe+Rect
if (-not [CursorProbe]::GetWindowRect($app.MainWindowHandle,[ref]$rect)) { throw 'Window bounds unavailable.' }
# Move to the application's empty title area, away from text and controls.
if (-not [CursorProbe]::SetCursorPos($rect.Left+250,$rect.Top+30)) { throw 'Could not position cursor.' }
Start-Sleep -Milliseconds 200
$measurement = [CursorProbe]::Measure($Seconds*1000,[bool]$KeepPointerActive,$rect.Left+250,$rect.Top+30,$ReferenceCore)
$after = Read-StatusText
$report = [pscustomobject]@{appId=$AppId;version=(Get-Item (Join-Path (Split-Path $app.Path) 'UUDisplaySwitch.dll')).VersionInfo.FileVersion;seconds=$Seconds;cursorVisibleEnough=($measurement.VisibleSamples -gt $measurement.Samples*0.9);before=$before;after=$after;cursor=$measurement}
$directory = Split-Path -Parent ([IO.Path]::GetFullPath($ReportPath))
New-Item -ItemType Directory -Path $directory -Force | Out-Null
$report | ConvertTo-Json -Depth 7 | Set-Content -LiteralPath $ReportPath -Encoding utf8
$report | ConvertTo-Json -Depth 7
