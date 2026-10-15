param([Parameter(Mandatory=$true)][int]$AppId,[Parameter(Mandatory=$true)][long]$MainHandle)
$ErrorActionPreference='Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
if(-not ('ActualTrayMouse' -as [type])){
    Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class ActualTrayMouse { [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y); [DllImport("user32.dll")] public static extern void mouse_event(uint f,uint x,uint y,uint d,UIntPtr e); [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h); }'
}
function Click-Element($item,[bool]$right,[string]$stage){
    if(-not $item -or $item.Current.IsOffscreen){throw "Tray target is not visible at $stage; found=$([bool]$item)"}
    $b=$item.Current.BoundingRectangle
    if($b.IsEmpty -or $b.Width -le 0 -or $b.Height -le 0){throw 'Tray target has no usable bounds'}
    [ActualTrayMouse]::SetCursorPos([int]($b.X+$b.Width/2),[int]($b.Y+$b.Height/2)) | Out-Null
    Start-Sleep -Milliseconds 100
    [ActualTrayMouse]::mouse_event($(if($right){8}else{2}),0,0,0,[UIntPtr]::Zero)
    [ActualTrayMouse]::mouse_event($(if($right){16}else{4}),0,0,0,[UIntPtr]::Zero)
}
$root=[System.Windows.Automation.AutomationElement]::RootElement
$children=[System.Windows.Automation.TreeScope]::Children
$descendants=[System.Windows.Automation.TreeScope]::Descendants
function Find-Class($name){$root.FindFirst($children,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ClassNameProperty,$name))}
function Find-Icon($scope){if($scope){$scope.FindFirst($descendants,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::NameProperty,'UU 虚拟屏'))}}
$tray=Find-Class 'Shell_TrayWnd'
$icon=Find-Icon $tray
if(-not $icon -or $icon.Current.IsOffscreen){
    $buttons=$tray.FindAll($descendants,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Button))
    $hidden=$buttons | Where-Object {$_.Current.Name -like 'Show Hidden Icons*'} | Select-Object -First 1
    if(-not $hidden){throw 'Explorer overflow toggle is unavailable'}
    # The XAML overflow window may remain in UIA after dismissal. Its toggle
    # exposes Hide only while expanded; do not trust stale provider bounds.
    if($hidden.Current.Name -notmatch '\sHide$'){
        Click-Element $hidden $false 'overflow button'
        Start-Sleep -Milliseconds 650
    }
    $overflow=Find-Class 'TopLevelWindowForOverflowXamlIsland'
    $icon=Find-Icon $overflow
    if(-not $icon){throw "Application icon unavailable; toggle=$($hidden.Current.Name); overflow=$([bool]$overflow)"}
}
Click-Element $icon $true 'application icon'
Start-Sleep -Milliseconds 500
$menus=$root.FindAll($children,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::ClassNameProperty,'#32768'))
$menu=$menus | Where-Object {$_.Current.ProcessId -eq $AppId} | Select-Object -First 1
[pscustomobject]@{realRightClick=$true;mainVisible=[ActualTrayMouse]::IsWindowVisible([IntPtr]$MainHandle);menuVisible=[bool]$menu;menuHwnd=$(if($menu){$menu.Current.NativeWindowHandle}else{0})}
