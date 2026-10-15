param([Parameter(Mandatory=$true)][int]$AppId,[Parameter(Mandatory=$true)][string]$ReportDirectory)
$ErrorActionPreference='Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Drawing
Add-Type -TypeDefinition @'
using System; using System.Text; using System.Runtime.InteropServices;
public static class ActualMenuTest {
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left,Top,Right,Bottom; }
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
 [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern uint RegisterWindowMessageW(string s);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")] public static extern void mouse_event(uint f,uint x,uint y,uint d,UIntPtr e);
 [DllImport("user32.dll")] public static extern void keybd_event(byte k,byte s,uint f,UIntPtr e);
 [DllImport("user32.dll")] public static extern int GetMenuItemCount(IntPtr h);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetMenuStringW(IntPtr h,uint i,StringBuilder t,int c,uint f);
 [DllImport("user32.dll")] public static extern bool GetMenuItemRect(IntPtr w,IntPtr m,uint i,out Rect r);
 [DllImport("user32.dll")] public static extern uint GetGuiResources(IntPtr h,uint f);
}
'@
$application=Get-CimInstance Win32_Process -Filter "ProcessId=$AppId"
if($application.Name -ne 'UUDisplaySwitch.exe' -or $application.CommandLine -notmatch '\s--read-only(?:\s|$)'){throw 'This test only controls an explicitly read-only test application'}
$app=Get-Process -Id $AppId
$hwnd=$app.MainWindowHandle
if($hwnd -eq 0){throw 'Open the read-only test window before running this test'}
$handle=$app.Handle
$window=[System.Windows.Automation.AutomationElement]::FromHandle($hwnd)
$root=[System.Windows.Automation.AutomationElement]::RootElement
$descendants=[System.Windows.Automation.TreeScope]::Descendants
New-Item -ItemType Directory -Path $ReportDirectory -Force | Out-Null
function Find-Id($id){$window.FindFirst($descendants,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::AutomationIdProperty,$id))}
function Get-OwnedMenu {
    $menus=$root.FindAll([System.Windows.Automation.TreeScope]::Children,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ClassNameProperty,'#32768'))
    $menus | Where-Object {$_.Current.ProcessId -eq $AppId} | Select-Object -First 1
}
function Read-Menu($menuHwnd){
    $menu=[ActualMenuTest]::SendMessage([IntPtr]$menuHwnd,0x1e1,[IntPtr]::Zero,[IntPtr]::Zero)
    if([ActualMenuTest]::GetMenuItemCount($menu) -ne 2){throw 'Expected two menu items'}
    $items=@()
    foreach($index in 0,1){
        $text=[Text.StringBuilder]::new(256)
        [void][ActualMenuTest]::GetMenuStringW($menu,$index,$text,256,0x400)
        $rect=[ActualMenuTest+Rect]::new()
        if(-not [ActualMenuTest]::GetMenuItemRect([IntPtr]::Zero,$menu,$index,[ref]$rect)){throw 'Menu item bounds unavailable'}
        $items+=[pscustomobject]@{label=$text.ToString();rect=$rect}
    }
    if($items[0].label -ne '打开 UU 虚拟屏' -or $items[1].label -ne '退出程序并停止监测'){throw 'Unexpected native menu labels'}
    return $items
}
function Click-MenuItem($item){
    $b=$item.rect
    if($b.Right -le $b.Left -or $b.Bottom -le $b.Top){throw 'Invalid menu bounds'}
    [ActualMenuTest]::SetCursorPos([int](($b.Left+$b.Right)/2),[int](($b.Top+$b.Bottom)/2)) | Out-Null
    [ActualMenuTest]::mouse_event(2,0,0,0,[UIntPtr]::Zero)
    [ActualMenuTest]::mouse_event(4,0,0,0,[UIntPtr]::Zero)
}
function Open-RealMenu {
    $result=& (Join-Path $PSScriptRoot 'Invoke-TrayRightClick.ps1') -AppId $AppId -MainHandle $hwnd
    if($result.mainVisible -or -not $result.menuVisible){throw "Real right-click failed: $($result | ConvertTo-Json -Compress)"}
    return $result
}
$results=@()
$callback=[ActualMenuTest]::RegisterWindowMessageW('UUDisplaySwitch.TrayCallback.v1')
try {
    foreach($theme in @(@{key='dark';label='深色'},@{key='light';label='浅色'})){
        (Find-Id 'settings-nav').GetCurrentPattern([System.Windows.Automation.SelectionItemPattern]::Pattern).Select()
        (Find-Id 'theme-picker').GetCurrentPattern([System.Windows.Automation.ExpandCollapsePattern]::Pattern).Expand()
        Start-Sleep -Milliseconds 150
        $choice=$window.FindFirst($descendants,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::NameProperty,$theme.label))
        if(-not $choice){throw 'Theme choice unavailable'}
        $choice.GetCurrentPattern([System.Windows.Automation.SelectionItemPattern]::Pattern).Select()
        Start-Sleep -Milliseconds 250
        [ActualMenuTest]::PostMessage($hwnd,0x10,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
        Start-Sleep -Milliseconds 600
        if([ActualMenuTest]::IsWindowVisible($hwnd)){throw 'Main window did not hide'}
        $counts=@()
        for($attempt=1;$attempt -le 6;$attempt++){
            $menuState=Open-RealMenu
            $menu=Get-OwnedMenu
            $items=Read-Menu $menuState.menuHwnd
            # Deliberately deliver an additional callback while TrackPopupMenuEx is active.
            [ActualMenuTest]::PostMessage($hwnd,$callback,[IntPtr]13107400,[IntPtr]0x1007b) | Out-Null
            Start-Sleep -Milliseconds 100
            $same=Get-OwnedMenu
            if(-not $same -or $same.Current.NativeWindowHandle -ne $menuState.menuHwnd -or [ActualMenuTest]::IsWindowVisible($hwnd)){throw 'Duplicate callback replaced the menu or restored the main window'}
            if($attempt -eq 1){
                $b=$menu.Current.BoundingRectangle
                $bitmap=[System.Drawing.Bitmap]::new([int]$b.Width,[int]$b.Height)
                $graphics=[System.Drawing.Graphics]::FromImage($bitmap)
                try {
                    $graphics.CopyFromScreen([int]$b.X,[int]$b.Y,0,0,$bitmap.Size)
                    $color=$bitmap.GetPixel(10,10)
                    if(($theme.key -eq 'dark' -and $color.R -gt 100) -or ($theme.key -eq 'light' -and $color.R -lt 180)){throw "Incorrect menu theme pixel: $color"}
                    $bitmap.Save((Join-Path $ReportDirectory "actual-tray-$($theme.key).png"))
                } finally {$graphics.Dispose();$bitmap.Dispose()}
            }
            [ActualMenuTest]::keybd_event(0x1b,0,0,[UIntPtr]::Zero)
            [ActualMenuTest]::keybd_event(0x1b,0,2,[UIntPtr]::Zero)
            Start-Sleep -Milliseconds 250
            if((Get-OwnedMenu) -or [ActualMenuTest]::IsWindowVisible($hwnd)){throw 'Dismissal left a menu or restored the main window'}
            $counts += [ActualMenuTest]::GetGuiResources($handle,0)
            # Close the Explorer overflow flyout as well, then wait for its
            # animation before resolving fresh screen bounds on the next click.
            [ActualMenuTest]::keybd_event(0x1b,0,0,[UIntPtr]::Zero)
            [ActualMenuTest]::keybd_event(0x1b,0,2,[UIntPtr]::Zero)
            Start-Sleep -Milliseconds 500
        }
        if(($counts | Measure-Object -Maximum).Maximum - ($counts | Measure-Object -Minimum).Minimum -gt 2){throw "GDI objects grew during repeated menu operations: $counts"}
        $results+=[pscustomobject]@{theme=$theme.key;realRightClicks=6;duplicateCallbacks=6;cancelAndReopenPassed=$true;mainStayedHidden=$true;gdiCounts=$counts;pixel=$color.ToString()}
        $menuState=Open-RealMenu
        $items=Read-Menu $menuState.menuHwnd
        Click-MenuItem $items[0]
        Start-Sleep -Milliseconds 400
        if(-not [ActualMenuTest]::IsWindowVisible($hwnd)){throw 'Show-window menu command failed'}
    }
    [ActualMenuTest]::PostMessage($hwnd,0x10,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
    Start-Sleep -Milliseconds 600
    $menuState=Open-RealMenu
    $items=Read-Menu $menuState.menuHwnd
    Click-MenuItem $items[1]
    if(-not $app.WaitForExit(8000) -or $app.ExitCode -ne 0){throw 'Real tray Exit command did not close the read-only app cleanly'}
    [pscustomobject]@{passed=$true;appId=$AppId;results=$results;showAndExitPassed=$true} | ConvertTo-Json -Depth 7 | Set-Content (Join-Path $ReportDirectory 'actual-tray-result.json') -Encoding UTF8
} catch {
    [pscustomobject]@{passed=$false;error=$_.ToString();stack=$_.ScriptStackTrace;results=$results} | ConvertTo-Json -Depth 7 | Set-Content (Join-Path $ReportDirectory 'actual-tray-result.json') -Encoding UTF8
    throw
}
