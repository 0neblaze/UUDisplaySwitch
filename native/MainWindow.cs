using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Windowing;
using Windows.Graphics;
using Windows.UI;
using System.Diagnostics;
using System.Text.Json;
using Microsoft.UI.Xaml.Markup;
using Microsoft.UI.Xaml.Media.Imaging;
using Windows.Graphics.Imaging;
using Windows.Storage;
using System.Runtime.InteropServices.WindowsRuntime;
using System.Runtime.InteropServices;
using Microsoft.UI.Composition.SystemBackdrops;

namespace UUDisplaySwitch;
public sealed class MainWindow:Window
{
    Prefs prefs=new(); CoreState state=new(); bool busy,refreshing,building,automationBusy,startupBusy,startupKnown,startupEnabled; string page="overview"; string? initError;string appliedBackdrop="";
    readonly Grid root=new(); readonly NavigationView navigation=new(){IsBackButtonVisible=NavigationViewBackButtonVisible.Collapsed,IsSettingsVisible=true,PaneDisplayMode=NavigationViewPaneDisplayMode.Auto,ExpandedModeThresholdWidth=1000,CompactModeThresholdWidth=600,OpenPaneLength=216};
    readonly NavigationViewItem overviewNav=new(){Icon=new SymbolIcon(Symbol.Home)};
    readonly TextBlock titleText=new(){FontSize=13,VerticalAlignment=VerticalAlignment.Center};
    readonly StackPanel content=new(){Spacing=18,MaxWidth=1040,HorizontalAlignment=HorizontalAlignment.Stretch};
    readonly SolidColorBrush navigationRootBrush=new(Color.FromArgb(0,0,0,0));
    readonly SolidColorBrush navigationContentBrush=new(Color.FromArgb(0,0,0,0));
    readonly SolidColorBrush navigationPaneBrush=new(Color.FromArgb(0,0,0,0));
    Grid? navigationContentGrid;
    readonly List<Grid> settingsRows=new(); ComboBox? themePicker,languagePicker,backdropPicker;ToggleSwitch? startupToggle;
    readonly ScrollViewer scroll=new(){HorizontalScrollBarVisibility=ScrollBarVisibility.Disabled,VerticalScrollBarVisibility=ScrollBarVisibility.Auto};
    readonly DispatcherTimer timer=new(){Interval=TimeSpan.FromSeconds(3)};
    readonly MessagePanel info=new();
    readonly MessagePanel monitorInfo=new();string? lastMonitorDiagnostic;
    TrayIcon? tray;bool allowClose,exiting;ToggleSwitch? autoExitToggle;
    long autoExitEnabledAfter=DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();bool checkingAutoExit;
    TextBlock? statusTitle,statusDetail,lastSync,monitorStatus;Button? enableButton,disableButton,startButton;ToggleSwitch? autoToggle; Grid? actions;
    public string T(string key)=>Strings.Get(key,prefs.Language);
    public MainWindow(){
        App.Trace("constructor begin");
        navigation.Resources["NavigationViewContentBackground"]=navigationContentBrush;
        navigation.Resources["NavigationViewExpandedPaneBackground"]=navigationPaneBrush;
        navigation.Background=navigationRootBrush;
        scroll.Background=navigationContentBrush;
        navigation.Loaded+=(_,_)=>ApplyNavigationSurface();
        navigation.LayoutUpdated+=(_,_)=>ApplyNavigationSurface();
        try{prefs=Prefs.Load();}catch(Exception e){initError=e.Message;}
        Title=T("app");
        AppWindow.SetIcon(Path.Combine(AppContext.BaseDirectory,"app.ico"));
        root.RowDefinitions.Add(new(){Height=new GridLength(48)});root.RowDefinitions.Add(new(){Height=new GridLength(1,GridUnitType.Star)});
        var titlebar=new Grid(){Padding=new Thickness(20,0,140,0)};
        titleText.Text=T("app");titlebar.Children.Add(titleText);root.Children.Add(titlebar);
        ExtendsContentIntoTitleBar=true;SetTitleBar(titlebar);
        AutomationProperties.SetAutomationId(overviewNav,"overview-nav");
        navigation.MenuItems.Add(overviewNav);
        navigation.ItemInvoked+=(_,e)=>{page=e.IsSettingsInvoked?"settings":"overview";BuildPage();};
        scroll.Content=content;navigation.Content=scroll;Grid.SetRow(navigation,1);root.Children.Add(navigation);Content=root;
        root.SizeChanged+=(_,_)=>{if(!building)ApplyLayout();};
        ResizePreferred();
        ApplyTheme();
        timer.Tick+=async(_,_)=>await Refresh();timer.Start();
        AppWindow.Closing+=(_,e)=>{if(!allowClose&&!App.Validate&&tray!=null){e.Cancel=true;AppWindow.Hide();}};
        Closed+=(_,_)=>{timer.Stop();try{tray?.Dispose();}catch(Exception e){App.Trace("Tray cleanup: "+e);}tray=null;};
        root.Loaded+=(_,_)=>{if(!App.Validate&&tray==null)try{tray=new TrayIcon(WinRT.Interop.WindowNative.GetWindowHandle(this),Path.Combine(AppContext.BaseDirectory,"app.ico"),TrayLabels(),ShowWindow,()=>{DispatcherQueue.TryEnqueue(async()=>await ExitFromTray());},e=>{App.Record("tray-error",new{e.Message,exception=e.ToString()});ShowWindow();ShowInfo(T("trayError"),e.Message,InfoBarSeverity.Error);});tray.UpdateTheme(Dark);}catch(Exception e){App.Record("tray-error",new{e.Message,exception=e.ToString()});ShowInfo(T("trayError"),e.Message,InfoBarSeverity.Error);}};
        root.Loaded+=async(_,_)=>{App.Trace("root loaded");BuildPage();App.Trace("page built");await Refresh(true);App.Trace("refresh done");if(initError!=null)ShowInfo(T("loadError"),initError,InfoBarSeverity.Error);if(App.Validate){try{if(App.ValidateMonitor)await ValidateMonitorUI();else await ValidateUI();}catch(Exception e){File.WriteAllText(Path.Combine(Prefs.DataDir,"ui-validation-failure.txt"),e.ToString());Environment.ExitCode=1;Close();}}};
        root.ActualThemeChanged+=(_,_)=>{if(root.IsLoaded&&!building&&prefs.Theme=="system"){ApplyTheme();BuildPage();}};
        root.Loaded+=async(_,_)=>{if(App.ValidateTray)try{await Task.Delay(700);await ValidateTray();}catch(Exception e){File.WriteAllText(Path.Combine(Prefs.DataDir,"tray-validation-failure.txt"),e.ToString());Environment.ExitCode=1;allowClose=true;Close();}};
        App.Trace("constructor end");
    }
    [DllImport("user32.dll")]static extern uint GetDpiForWindow(IntPtr hwnd);
    [DllImport("user32.dll")]static extern bool SetForegroundWindow(IntPtr hwnd);
    [DllImport("user32.dll",SetLastError=true)]static extern bool PostMessage(IntPtr hwnd,uint message,IntPtr wParam,IntPtr lParam);
    TrayIconLabels TrayLabels()=>new(T("app"),T("trayShow"),T("trayExit"));
    void ShowWindow(){AppWindow.Show();if(AppWindow.Presenter is OverlappedPresenter presenter)presenter.Restore();Activate();SetForegroundWindow(WinRT.Interop.WindowNative.GetWindowHandle(this));}
    async Task ExitFromTray(){
        App.Trace("Tray exit requested");
        if(exiting)return;if(busy||automationBusy||startupBusy){ShowWindow();ShowInfo(T("error"),T("exitBusy"),InfoBarSeverity.Warning);return;}
        exiting=true;
        try{if(!App.ReadOnly)await Core.StopWatcher();allowClose=true;App.Trace("Tray closing window");Close();App.Trace("Tray exiting application");Application.Current.Exit();}
        catch(Exception e){ShowWindow();ShowInfo(T("error"),e.Message,InfoBarSeverity.Error);}
        finally{exiting=false;}
    }
    async Task ExitAfterUU(){
        if(checkingAutoExit||exiting)return;checkingAutoExit=true;
        try{var confirmed=await Core.Status();if(AutomationPolicy.ShouldExit(prefs,confirmed,autoExitEnabledAfter)){App.Record("auto-exit",new{confirmed.UuExitAt,confirmed.UuRunning,confirmed.Status,autoExitEnabledAfter});await ExitFromTray();}}
        catch(Exception e){ShowInfo(T("monitorError"),e.Message,InfoBarSeverity.Error);App.Record("auto-exit-error",new{e.Message});}
        finally{checkingAutoExit=false;}
    }
    async Task ValidateTray(){
        if(!App.ReadOnly||tray==null)throw new InvalidOperationException("Read-only tray was not initialized.");
        if(!PostMessage(WinRT.Interop.WindowNative.GetWindowHandle(this),0x0010,IntPtr.Zero,IntPtr.Zero))throw new InvalidOperationException("Could not request window close.");await Task.Delay(200);if(AppWindow.IsVisible)throw new InvalidOperationException("Close did not hide to tray.");
        tray.DispatchMenuCommand(TrayIcon.MenuShowWindowCommand);await Task.Delay(200);if(!AppWindow.IsVisible)throw new InvalidOperationException("Tray show failed.");
        foreach(var language in new[]{"zh-CN","zh-TW","en"}){prefs.Language=language;tray.UpdateLabels(TrayLabels());}
        int show=0,exit=0;TrayIcon.DispatchMenuCommand(TrayIcon.MenuShowWindowCommand,()=>show++,()=>exit++);TrayIcon.DispatchMenuCommand(TrayIcon.MenuExitCommand,()=>show++,()=>exit++);
        if(show!=1||exit!=1||TrayIcon.DispatchMenuCommand(999,()=>show++,()=>exit++))throw new InvalidOperationException("Tray command routing failed.");
        File.WriteAllText(Path.Combine(Prefs.DataDir,"tray-validation.json"),JsonSerializer.Serialize(new{registered=true,hideToTray=true,restore=true,localizedLabels=3,menuRouting=true,readOnly=true}));
        tray.DispatchMenuCommand(TrayIcon.MenuExitCommand);
    }
    void ResizePreferred(){var area=DisplayArea.GetFromWindowId(AppWindow.Id,DisplayAreaFallback.Primary).WorkArea;double scale=GetDpiForWindow(WinRT.Interop.WindowNative.GetWindowHandle(this))/96.0;int w=Math.Min((int)(1100*scale),area.Width-32),h=Math.Min((int)(820*scale),area.Height-32);AppWindow.Resize(new SizeInt32(w,h));AppWindow.Move(new PointInt32(area.X+(area.Width-w)/2,area.Y+(area.Height-h)/2));}
    TextBlock Text(string value,double size=14,bool secondary=false)=>new(){Text=value,FontSize=size,TextWrapping=TextWrapping.Wrap,Opacity=secondary?0.72:1};
    FontIcon Icon(string glyph,double size=20)=>new(){Glyph=glyph,FontSize=size,VerticalAlignment=VerticalAlignment.Center};
    bool Dark=>prefs.Theme=="dark"||(prefs.Theme=="system"&&root.ActualTheme==ElementTheme.Dark);
    SolidColorBrush ColorBrush(byte a,byte r,byte g,byte b)=>new(Color.FromArgb(a,r,g,b));
    Border Card(UIElement child)=>new(){Background=Dark?ColorBrush((byte)(SystemBackdrop==null?255:190),43,43,43):ColorBrush((byte)(SystemBackdrop==null?255:210),255,255,255),BorderBrush=Dark?ColorBrush(255,56,56,56):ColorBrush(255,229,229,229),BorderThickness=new Thickness(1),CornerRadius=new CornerRadius(8),Padding=new Thickness(22),Child=child};
    StackPanel Stack(params UIElement[] children){var p=new StackPanel(){Spacing=8};foreach(var c in children)p.Children.Add(c);return p;}
    Button Btn(string text,Action action,bool accent=false){var b=new Button(){Content=text,MinHeight=36,Padding=new Thickness(17,7,17,7),HorizontalAlignment=HorizontalAlignment.Left};if(accent){b.Background=Dark?ColorBrush(255,96,205,255):ColorBrush(255,0,95,184);b.Foreground=Dark?ColorBrush(255,0,0,0):ColorBrush(255,255,255,255);}b.Click+=(_,_)=>action();return b;}
    void Section(string key){content.Children.Add(new TextBlock(){Text=T(key),FontSize=16,FontWeight=Microsoft.UI.Text.FontWeights.SemiBold,Margin=new Thickness(0,6,0,0)});}
    void ApplyTheme(){
        root.RequestedTheme=prefs.Theme=="dark"?ElementTheme.Dark:prefs.Theme=="light"?ElementTheme.Light:ElementTheme.Default;
        if(appliedBackdrop!=prefs.Backdrop){SystemBackdrop=null;if(prefs.Backdrop!="none"&&MicaController.IsSupported())SystemBackdrop=new MicaBackdrop(){Kind=prefs.Backdrop=="mica-alt"?MicaKind.BaseAlt:MicaKind.Base};appliedBackdrop=prefs.Backdrop;}
        bool hasBackdrop=SystemBackdrop!=null;
        navigationRootBrush.Color=hasBackdrop?Color.FromArgb(0,0,0,0):Dark?Color.FromArgb(255,32,32,32):Color.FromArgb(255,243,243,243);
        navigationContentBrush.Color=hasBackdrop?Color.FromArgb(0,0,0,0):Dark?Color.FromArgb(255,39,39,39):Color.FromArgb(255,249,249,249);
        navigationPaneBrush.Color=Color.FromArgb(0,0,0,0);
        root.Background=navigationRootBrush;
        navigation.Background=navigationRootBrush;
        scroll.Background=navigationContentBrush;
        ApplyNavigationSurface();
        tray?.UpdateTheme(Dark);
        AppWindow.TitleBar.PreferredTheme=prefs.Theme=="dark"?TitleBarTheme.Dark:prefs.Theme=="light"?TitleBarTheme.Light:TitleBarTheme.UseDefaultAppMode;
    }
    void ApplyNavigationSurface(){
        if(!navigation.IsLoaded)return;
        if(navigationContentGrid==null||!navigationContentGrid.IsLoaded)navigationContentGrid=FindNamedElement(navigation,"ContentGrid") as Grid;
        if(navigationContentGrid!=null&&!ReferenceEquals(navigationContentGrid.Background,navigationContentBrush))navigationContentGrid.Background=navigationContentBrush;
    }
    void ApplyLayout(){content.Margin=new Thickness(root.ActualWidth<640?18:32,22,root.ActualWidth<640?18:32,32);if(actions!=null)SetColumns(actions,root.ActualWidth<730);foreach(var row in settingsRows)SetColumns(row,root.ActualWidth<760,true);}
    void SetColumns(Grid grid,bool narrow,bool trailingAuto=false){bool wasNarrow=grid.RowDefinitions.Count==2;if(grid.ColumnDefinitions.Count>0&&wasNarrow==narrow)return;grid.ColumnDefinitions.Clear();grid.RowDefinitions.Clear();grid.ColumnDefinitions.Add(new());var second=(FrameworkElement)grid.Children[1];if(narrow){grid.RowDefinitions.Add(new(){Height=GridLength.Auto});grid.RowDefinitions.Add(new(){Height=GridLength.Auto});Grid.SetColumn(second,0);Grid.SetRow(second,1);second.HorizontalAlignment=trailingAuto?HorizontalAlignment.Left:HorizontalAlignment.Stretch;}else{grid.ColumnDefinitions.Add(new(){Width=trailingAuto?GridLength.Auto:new GridLength(1,GridUnitType.Star)});Grid.SetColumn(second,1);Grid.SetRow(second,0);second.HorizontalAlignment=trailingAuto?HorizontalAlignment.Right:HorizontalAlignment.Stretch;}}
    void BuildPage(){App.Trace("build begin");building=true;content.Children.Clear();settingsRows.Clear();actions=null;statusTitle=null;statusDetail=null;lastSync=null;monitorStatus=null;autoToggle=null;startupToggle=null;startButton=null;enableButton=null;disableButton=null;
        Title=T("app");titleText.Text=T("app");if(tray!=null)try{tray.UpdateLabels(TrayLabels());}catch(Exception e){initError=e.Message;}overviewNav.Content=T("overview");if(navigation.SettingsItem is NavigationViewItem settings){settings.Content=T("settings");AutomationProperties.SetAutomationId(settings,"settings-nav");AutomationProperties.SetName(settings,T("settings"));ToolTipService.SetToolTip(settings,T("settings"));}navigation.SelectedItem=page=="overview"?overviewNav:navigation.SettingsItem;
        App.Trace("build title");var title=new TextBlock(){Text=T(page),FontSize=28,FontWeight=Microsoft.UI.Text.FontWeights.SemiBold};AutomationProperties.SetAutomationId(title,"page-title");
        content.Children.Add(Stack(title,Text(T(page=="settings"?"settingsSubtitle":"tagline"),14,true)));
        App.Trace("build info");if(App.ReadOnly)content.Children.Add(Text(T("readOnly"),12,true));
        info.IsOpen=false;content.Children.Add(info);content.Children.Add(monitorInfo);
        App.Trace("build body");if(page=="settings")BuildSettings();else BuildOverview();
        App.Trace("build layout");building=false;ApplyLayout();UpdateState();
    }
    void BuildOverview(){
        App.Trace("hero grid");
        var hero=new Grid(){ColumnSpacing=18};hero.ColumnDefinitions.Add(new(){Width=new GridLength(52)});hero.ColumnDefinitions.Add(new());
        App.Trace("hero icon");
        var glyph=new Border(){Width=52,Height=52,CornerRadius=new CornerRadius(12),Background=Dark?ColorBrush(255,39,63,76):ColorBrush(255,230,240,250),VerticalAlignment=VerticalAlignment.Top,Child=Icon("\uE7F4",25)};
        hero.Children.Add(glyph);statusTitle=Text(T("loading"),24);statusTitle.FontWeight=Microsoft.UI.Text.FontWeights.SemiBold;AutomationProperties.SetAutomationId(statusTitle,"device-status");statusDetail=Text("",14,true);
        var heroText=Stack(Text(T("adapter"),13,true),statusTitle,statusDetail);Grid.SetColumn(heroText,1);hero.Children.Add(heroText);content.Children.Add(Card(hero));
        App.Trace("actions buttons");
        actions=new Grid(){ColumnSpacing=14,RowSpacing=14};
        enableButton=Btn(T("enable"),async()=>await Switch("enable"),true);disableButton=Btn(T("disable"),async()=>await Switch("disable"));AutomationProperties.SetAutomationId(enableButton,"enable-adapter");AutomationProperties.SetAutomationId(disableButton,"disable-adapter");
        var remote=Stack(Icon("\uE708",24),Text(T("remote"),18),Text(T("remoteDetail"),13,true),enableButton);remote.Spacing=13;
        var local=Stack(Icon("\uE7FC",24),Text(T("local"),18),Text(T("localDetail"),13,true),disableButton);local.Spacing=13;
        ((FrameworkElement)remote.Children[0]).HorizontalAlignment=HorizontalAlignment.Left;((FrameworkElement)local.Children[0]).HorizontalAlignment=HorizontalAlignment.Left;
        App.Trace("actions cards");
        actions.Children.Add(Card(remote));actions.Children.Add(Card(local));content.Children.Add(actions);
        var autoBox=Stack(Text(T("autoOverview"),16),Text(T("autoBrief"),13,true));monitorStatus=Text("",12,true);autoBox.Children.Add(monitorStatus);autoBox.Children.Add(Btn(T("configure"),()=>{page="settings";BuildPage();}));content.Children.Add(Card(autoBox));
        App.Trace("footer");
        var footer=new Grid();footer.ColumnDefinitions.Add(new());footer.ColumnDefinitions.Add(new(){Width=GridLength.Auto});lastSync=Text("",12,true);lastSync.VerticalAlignment=VerticalAlignment.Center;footer.Children.Add(lastSync);var refresh=Btn(T("refresh"),async()=>await Refresh(true));AutomationProperties.SetAutomationId(refresh,"refresh-status");Grid.SetColumn(refresh,1);footer.Children.Add(refresh);content.Children.Add(footer);
        var details=Stack(Text("GameViewer Virtual Display Adapter",13),Text(T("deviceID")+": ROOT\\DISPLAY\\0000",12,true),Text(T("disabledCode"),12,true),Text(T("scope"),12,true));
        App.Trace("expander");
        content.Children.Add(new Expander(){Header=T("deviceDetails"),HorizontalAlignment=HorizontalAlignment.Stretch,Content=details});
        App.Trace("overview done");
    }
    void BuildSettings(){
        Section("appearance");
        var theme=new ComboBox(){MinWidth=185,HorizontalAlignment=HorizontalAlignment.Left};theme.Items.Add(T("system"));theme.Items.Add(T("light"));theme.Items.Add(T("dark"));theme.SelectedIndex=prefs.Theme=="system"?0:prefs.Theme=="light"?1:2;AutomationProperties.SetAutomationId(theme,"theme-picker");
        theme.SelectionChanged+=(_,_)=>{if(building)return;var old=prefs.Theme;prefs.Theme=new[]{"system","light","dark"}[theme.SelectedIndex];if(SavePrefs()){ApplyTheme();BuildPage();}else prefs.Theme=old;};
        themePicker=theme;AutomationProperties.SetName(theme,T("theme"));AddSettingsRow("theme","themeHint",theme);
        backdropPicker=new ComboBox(){MinWidth=185,HorizontalAlignment=HorizontalAlignment.Left};foreach(var key in new[]{"solid","mica","micaAlt"})backdropPicker.Items.Add(T(key));backdropPicker.SelectedIndex=prefs.Backdrop=="mica"?1:prefs.Backdrop=="mica-alt"?2:0;AutomationProperties.SetAutomationId(backdropPicker,"backdrop-picker");AutomationProperties.SetName(backdropPicker,T("backdrop"));backdropPicker.SelectionChanged+=(_,_)=>{if(building)return;string old=prefs.Backdrop;prefs.Backdrop=new[]{"none","mica","mica-alt"}[backdropPicker.SelectedIndex];if(SavePrefs()){ApplyTheme();BuildPage();}else prefs.Backdrop=old;};AddSettingsRow("backdrop","backdropHint",backdropPicker);
        if(prefs.Backdrop!="none"&&!MicaController.IsSupported())content.Children.Add(Text(T("backdropFallback"),12,true));
        var lang=new ComboBox(){MinWidth=185,HorizontalAlignment=HorizontalAlignment.Left};lang.Items.Add("简体中文");lang.Items.Add("繁體中文");lang.Items.Add("English");lang.SelectedIndex=prefs.Language=="zh-CN"?0:prefs.Language=="zh-TW"?1:2;AutomationProperties.SetAutomationId(lang,"language-picker");
        lang.SelectionChanged+=(_,_)=>{if(building)return;var old=prefs.Language;prefs.Language=new[]{"zh-CN","zh-TW","en"}[lang.SelectedIndex];if(SavePrefs())BuildPage();else prefs.Language=old;};
        languagePicker=lang;AutomationProperties.SetName(lang,T("language"));AddSettingsRow("language","languageHint",lang);
        Section("automation");
        autoToggle=new ToggleSwitch(){OnContent=T("on"),OffContent=T("off"),IsOn=prefs.AutoDisable,IsEnabled=!App.ReadOnly&&!automationBusy};AutomationProperties.SetAutomationId(autoToggle,"auto-disable");AutomationProperties.SetName(autoToggle,T("auto"));autoToggle.Toggled+=async(_,_)=>{if(building)return;await ToggleAutomation();};
        AddSettingsRow("auto","autoHint",autoToggle);
        autoExitToggle=new ToggleSwitch(){OnContent=T("on"),OffContent=T("off"),IsOn=prefs.AutoExit,IsEnabled=!App.ReadOnly&&!automationBusy};AutomationProperties.SetAutomationId(autoExitToggle,"auto-exit");AutomationProperties.SetName(autoExitToggle,T("autoExit"));autoExitToggle.Toggled+=async(_,_)=>{if(!building)await ToggleAutomation(true);};AddSettingsRow("autoExit","autoExitHint",autoExitToggle);content.Children.Add(Text(T("permission"),12,true));
        startupToggle=new ToggleSwitch(){IsOn=startupEnabled,OnContent=T("on"),OffContent=T("off"),IsEnabled=!App.ReadOnly&&startupKnown&&!startupBusy};AutomationProperties.SetAutomationId(startupToggle,"startup-toggle");AutomationProperties.SetName(startupToggle,T("startup"));startupToggle.Toggled+=async(_,_)=>{if(!building)await ToggleStartup();};AddSettingsRow("startup","startupHint",startupToggle);content.Children.Add(Text(T("startupPermission"),12,true));
        monitorStatus=Text("",14);startButton=Btn(T("start"),async()=>await StartMonitoring());startButton.IsEnabled=prefs.MonitorEnabled&&!App.ReadOnly;
        content.Children.Add(Card(Stack(Text(T("monitor"),16),monitorStatus,Text(T("monitorHint"),13,true),startButton)));
        Section("about");content.Children.Add(Card(Stack(Text(T("version"),16),Text(T("aboutDetail"),13,true))));
    }
    void AddSettingsRow(string title,string hint,FrameworkElement control){var row=new Grid(){ColumnSpacing=24,RowSpacing=14};row.Children.Add(Stack(Text(T(title),16),Text(T(hint),13,true)));control.VerticalAlignment=VerticalAlignment.Center;row.Children.Add(control);settingsRows.Add(row);content.Children.Add(Card(row));}
    bool SavePrefs(){if(App.ReadOnly)return true;try{prefs.Save();return true;}catch(Exception e){ShowInfo(T("savedError"),e.Message,InfoBarSeverity.Error);return false;}}
    async Task Refresh(bool queryStartup=false){if(refreshing||busy)return;refreshing=true;try{state=await Core.Status();UpdateState();}catch(Exception e){state.Status="error";ShowInfo(T("error"),e.Message,InfoBarSeverity.Error);UpdateState();}finally{refreshing=false;}if(queryStartup&&!startupBusy){try{startupEnabled=await Task.Run(StartupManager.Read);startupKnown=true;building=true;if(startupToggle!=null)startupToggle.IsOn=startupEnabled;building=false;UpdateState();}catch(Exception e){startupKnown=false;ShowInfo(T("startupError"),e.Message,InfoBarSeverity.Error);UpdateState();}}}
    void UpdateState(){
        bool valid=state.Status=="enabled"||state.Status=="disabled";
        if(statusTitle!=null)statusTitle.Text=T(state.Status=="enabled"?"enabled":state.Status=="disabled"?"disabled":"unavailable");
        if(statusDetail!=null)statusDetail.Text=busy?T("busy"):valid?T(state.Status=="enabled"?"enabledDetail":"disabledDetail"):state.Message;
        if(enableButton!=null)enableButton.IsEnabled=valid&&state.Status!="enabled"&&!busy&&!App.ReadOnly;
        if(disableButton!=null)disableButton.IsEnabled=valid&&state.Status!="disabled"&&!busy&&!App.ReadOnly;
        if(lastSync!=null)lastSync.Text=T("synced")+" "+DateTime.Now.ToString("HH:mm:ss");
        if(monitorStatus!=null)monitorStatus.Text=T(state.WatcherStatus=="error"?"monitorError":state.WatcherActive?state.WatcherStatus:"stopped");
        if(startButton!=null)startButton.IsEnabled=prefs.MonitorEnabled&&!state.WatcherActive&&!App.ReadOnly&&!automationBusy;
        if(autoToggle!=null)autoToggle.IsEnabled=!App.ReadOnly&&!automationBusy;
        if(autoExitToggle!=null)autoExitToggle.IsEnabled=!App.ReadOnly&&!automationBusy;
        if(!App.ReadOnly&&AutomationPolicy.ShouldExit(prefs,state,autoExitEnabledAfter)&&!exiting&&!busy&&!automationBusy&&!startupBusy)_ = ExitAfterUU();
        if(startupToggle!=null)startupToggle.IsEnabled=!App.ReadOnly&&startupKnown&&!startupBusy;
        if(state.GameRunning&&!busy&&!info.IsOpen)ShowInfo("",T("game"),InfoBarSeverity.Informational);
        UpdateMonitorInfo();
    }
    void UpdateMonitorInfo(){
        bool failed=state.WatcherStatus=="error";
        monitorInfo.Title=T("monitorError");monitorInfo.Message=state.WatcherError;monitorInfo.Severity=InfoBarSeverity.Warning;monitorInfo.IsOpen=failed;
        string? diagnostic=failed?state.WatcherError:null;
        if(diagnostic!=lastMonitorDiagnostic){
            if(!App.ReadOnly)App.Record(failed?"monitor-status-error":"monitor-status-recovered",new{state.WatcherActive,state.WatcherStatus,state.WatcherError,state.UuStateKnown,state.UuRunning});
            lastMonitorDiagnostic=diagnostic;
        }
    }
    void ValidateMonitorInfo(){
        var saved=state;
        try{
            ShowInfo("Other operation","Keep this message",InfoBarSeverity.Error);
            state=new(){WatcherStatus="error",WatcherError="Monitor heartbeat has an invalid process identity"};UpdateState();
            if(!monitorInfo.IsOpen)throw new InvalidOperationException("Monitor error was not displayed");
            state=new(){WatcherActive=true,WatcherStatus="armed"};UpdateState();
            if(monitorInfo.IsOpen||monitorStatus?.Text!=T("armed"))throw new InvalidOperationException("Recovered monitor retained a stale error");
            if(!info.IsOpen||info.Message!="Keep this message")throw new InvalidOperationException("Monitor recovery cleared an unrelated error");
            state=new(){WatcherStatus="error",WatcherError="Another monitor error"};UpdateState();
            if(!monitorInfo.IsOpen||monitorInfo.Message!="Another monitor error")throw new InvalidOperationException("Repeated monitor error was lost");
            state=new(){WatcherStatus="stopped"};UpdateState();
            if(monitorInfo.IsOpen)throw new InvalidOperationException("Stopped monitor retained an old error");
        }finally{state=saved;lastMonitorDiagnostic=null;info.IsOpen=false;UpdateState();}
    }
    async Task ValidateMonitorUI(){
        if(!App.ReadOnly)return;timer.Stop();ValidateMonitorInfo();
        foreach(var language in new[]{"zh-CN","zh-TW","en"})foreach(var theme in new[]{"light","dark"}){
            prefs.Language=language;prefs.Theme=theme;ApplyTheme();BuildPage();ValidateMonitorInfo();
        }
        prefs.Language="zh-CN";prefs.Theme="dark";ApplyTheme();BuildPage();
        state=new(){Status="disabled",WatcherActive=true,WatcherStatus="error",WatcherError="Monitor heartbeat has an invalid process identity"};UpdateState();await Task.Delay(200);await Capture("monitor-error.png");
        state=new(){Status="disabled",WatcherActive=true,WatcherStatus="armed",UuStateKnown=true,UuRunning=true};UpdateState();await Task.Delay(200);await Capture("monitor-recovered.png");
        File.WriteAllText(Path.Combine(Prefs.DataDir,"monitor-ui-validation.json"),JsonSerializer.Serialize(new{readOnly=true,languages=3,themes=2,errorShown=true,recoveryClearsError=true,unrelatedErrorPreserved=true,recurringErrorShown=true,stoppedClearsError=true}));
        Close();
    }
    void ShowInfo(string title,string message,InfoBarSeverity severity){info.Title=title;info.Message=message;info.Severity=severity;info.IsOpen=true;}
    async Task Switch(string mode){
        if(busy||App.ReadOnly)return;
        busy=true;UpdateState();try{if(mode=="disable"){var dialog=new ContentDialog(){XamlRoot=root.XamlRoot,RequestedTheme=root.RequestedTheme,Title=T("confirmTitle"),Content=Text(T("confirmBody")),PrimaryButtonText=T("confirm"),CloseButtonText=T("cancel"),DefaultButton=ContentDialogButton.Close};if(await dialog.ShowAsync()!=ContentDialogResult.Primary)return;}await Core.Switch(mode);state=await Core.Status();if(state.Status!=(mode=="enable"?"enabled":"disabled"))throw new IOException(state.Message);ShowInfo(T("success"),T(mode=="enable"?"enabledDetail":"localDetail"),InfoBarSeverity.Success);}catch(Exception e){ShowInfo(T("error"),e.Message,InfoBarSeverity.Error);}finally{busy=false;UpdateState();}
    }
    async Task ToggleAutomation(bool exitOption=false){
        if(autoToggle==null||autoExitToggle==null||App.ReadOnly||automationBusy)return;
        bool oldDisable=prefs.AutoDisable,oldExit=prefs.AutoExit;
        long oldEnabledAfter=autoExitEnabledAfter;
        if(exitOption)prefs.AutoExit=autoExitToggle.IsOn;else prefs.AutoDisable=autoToggle.IsOn;
        if(!oldExit&&prefs.AutoExit)autoExitEnabledAfter=DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
        automationBusy=true;UpdateState();
        try{prefs.Save();state=await Core.Status();if(prefs.MonitorEnabled&&!state.WatcherActive)state=await Core.StartWatcher();App.Record("automation-option",new{prefs.AutoDisable,prefs.AutoExit,state.WatcherActive,state.WatcherError});}
        catch(Exception e){prefs.AutoDisable=oldDisable;prefs.AutoExit=oldExit;autoExitEnabledAfter=oldEnabledAfter;string message=e.Message;try{prefs.Save();}catch(Exception saveError){message+="\n"+T("savedError")+" "+saveError.Message;}building=true;autoToggle.IsOn=oldDisable;autoExitToggle.IsOn=oldExit;building=false;ShowInfo(T("error"),message,InfoBarSeverity.Error);App.Record("monitor-start-error",new{message});}
        finally{automationBusy=false;UpdateState();}
    }
    async Task StartMonitoring(){if(App.ReadOnly||automationBusy)return;automationBusy=true;UpdateState();try{state=await Core.StartWatcher();App.Record("monitor-started",new{state.WatcherActive,state.WatcherStatus});}catch(Exception e){ShowInfo(T("error"),e.Message,InfoBarSeverity.Error);App.Record("monitor-start-error",new{e.Message});}finally{automationBusy=false;UpdateState();}}
    async Task ToggleStartup(){if(App.ReadOnly||startupBusy||startupToggle==null||!startupKnown)return;bool requested=startupToggle.IsOn;startupBusy=true;UpdateState();try{await StartupManager.Change(requested);startupEnabled=requested;ShowInfo(T("startup"),T(requested?"startupOn":"startupOff"),InfoBarSeverity.Success);}catch(Exception e){ShowInfo(T("startupError"),e.Message,InfoBarSeverity.Error);}finally{startupBusy=false;building=true;if(startupToggle!=null)startupToggle.IsOn=startupEnabled;building=false;UpdateState();}}
    async Task ValidateUI(){
        // Read-only layout exercises; neither the device nor monitor is touched.
        if(!App.ReadOnly)return;timer.Stop();var reports=new List<object>();AppWindow.Hide();AutomationPolicy.Validate();await Core.ValidateMonitorStartup();ValidateMonitorInfo();
        var startupValidation=await Task.Run(StartupManager.ValidatePlan);File.WriteAllText(Path.Combine(Prefs.DataDir,"startup-validation.json"),JsonSerializer.Serialize(startupValidation));
        page="settings";BuildPage();themePicker!.SelectedIndex=2;if(prefs.Theme!="dark")throw new InvalidOperationException("Theme interaction failed");languagePicker!.SelectedIndex=2;if(prefs.Language!="en"||titleText.Text!="UU Display")throw new InvalidOperationException("Language interaction failed");languagePicker!.SelectedIndex=1;if(prefs.Language!="zh-TW")throw new InvalidOperationException("Traditional language interaction failed");if(autoToggle!.IsEnabled||startButton!.IsEnabled)throw new InvalidOperationException("Read-only protection failed");
        if(startupToggle!.IsEnabled)throw new InvalidOperationException("Read-only startup protection failed");backdropPicker!.SelectedIndex=1;if(prefs.Backdrop!="mica")throw new InvalidOperationException("Mica selection failed");backdropPicker!.SelectedIndex=2;if(prefs.Backdrop!="mica-alt")throw new InvalidOperationException("Mica Alt selection failed");backdropPicker!.SelectedIndex=0;if(SystemBackdrop!=null)throw new InvalidOperationException("Solid background failed");
        foreach(var lang in new[]{"zh-CN","zh-TW","en"})foreach(var theme in new[]{"light","dark"})foreach(var material in new[]{"none","mica","mica-alt"})foreach(var view in new[]{"overview","settings"}){
            prefs.Language=lang;prefs.Theme=theme;prefs.Backdrop=material;ApplyTheme();page=view;BuildPage();
            if(material!="none"&&SystemBackdrop!=null){root.UpdateLayout();ValidateMicaSurface();}
            foreach(var size in new[]{new SizeInt32(640,650),new SizeInt32(980,780),new SizeInt32(1920,1080),new SizeInt32(2560,1440),new SizeInt32(3840,2160),new SizeInt32(1280,1040),new SizeInt32(2400,300)}){
                AppWindow.Resize(size);await Task.Delay(100);root.UpdateLayout();var clipped=new List<object>();CheckBounds(content,clipped);reports.Add(new{lang,theme,material,view,width=size.Width,height=size.Height,actualWidth=root.ActualWidth,contentWidth=content.ActualWidth,scrollable=scroll.ScrollableHeight,overflow=content.ActualWidth>scroll.ActualWidth+1,clipped});
            }
        }
        prefs=new();prefs.Theme="light";ApplyTheme();page="overview";BuildPage();ResizePreferred();AppWindow.Show();await Task.Delay(500);
        string p=Path.Combine(Prefs.DataDir,"ui-validation.json");File.WriteAllText(p,JsonSerializer.Serialize(reports));
        await Capture("overview-light.png");prefs.Theme="dark";ApplyTheme();BuildPage();await Task.Delay(300);await Capture("overview-dark.png");page="settings";BuildPage();await Task.Delay(300);await Capture("settings-dark.png");prefs.Theme="light";ApplyTheme();BuildPage();await Task.Delay(300);await Capture("settings-light.png");
        prefs.Language="en";BuildPage();await Task.Delay(200);await Capture("settings-english.png");prefs.Language="zh-TW";BuildPage();await Task.Delay(200);await Capture("settings-traditional.png");
        prefs.Language="zh-CN";prefs.Backdrop="mica";ApplyTheme();BuildPage();await Task.Delay(300);await Capture("settings-mica.png");prefs.Backdrop="mica-alt";prefs.Theme="dark";ApplyTheme();BuildPage();await Task.Delay(300);await Capture("settings-mica-alt.png");
        page="overview";BuildPage();Close();
    }
    void ValidateMicaSurface(){
        var contentGrid=FindNamedElement(navigation,"ContentGrid");
        if(contentGrid is not Grid grid||grid.Background is not SolidColorBrush background||background.Color.A!=0)
            throw new InvalidOperationException("Mica content surface is still opaque");
        if(navigation.Background is not SolidColorBrush navigationBackground||navigationBackground.Color.A!=0)
            throw new InvalidOperationException("Mica NavigationView surface is still opaque");
        if(scroll.Background is not SolidColorBrush scrollBackground||scrollBackground.Color.A!=0)
            throw new InvalidOperationException("Mica scroll surface is still opaque");
    }
    FrameworkElement? FindNamedElement(DependencyObject node,string name){
        if(node is FrameworkElement element&&element.Name==name)return element;
        for(int i=0;i<VisualTreeHelper.GetChildrenCount(node);i++){
            var match=FindNamedElement(VisualTreeHelper.GetChild(node,i),name);
            if(match!=null)return match;
        }
        return null;
    }
    void CheckBounds(DependencyObject node,List<object> failures){if(node is FrameworkElement element&&element.Visibility==Visibility.Visible&&(node is TextBlock||node is Button||node is ComboBox||node is ToggleSwitch)){var b=element.TransformToVisual(scroll).TransformBounds(new Windows.Foundation.Rect(0,0,element.ActualWidth,element.ActualHeight));if(b.Left < -1||b.Right>scroll.ActualWidth+1)failures.Add(new{type=node.GetType().Name,id=AutomationProperties.GetAutomationId(element),left=b.Left,right=b.Right,viewport=scroll.ActualWidth});}for(int i=0;i<VisualTreeHelper.GetChildrenCount(node);i++)CheckBounds(VisualTreeHelper.GetChild(node,i),failures);}
    async Task Capture(string name){var bitmap=new RenderTargetBitmap();await bitmap.RenderAsync(root);var pixels=(await bitmap.GetPixelsAsync()).ToArray();var folder=await StorageFolder.GetFolderFromPathAsync(Prefs.DataDir);var file=await folder.CreateFileAsync(name,CreationCollisionOption.ReplaceExisting);using var stream=await file.OpenAsync(FileAccessMode.ReadWrite);var encoder=await BitmapEncoder.CreateAsync(BitmapEncoder.PngEncoderId,stream);encoder.SetPixelData(BitmapPixelFormat.Bgra8,BitmapAlphaMode.Premultiplied,(uint)bitmap.PixelWidth,(uint)bitmap.PixelHeight,96,96,pixels);await encoder.FlushAsync();}
}
public sealed class MessagePanel:UserControl
{
    readonly TextBlock text=new(){TextWrapping=TextWrapping.Wrap};
    public string Title {get;set;}="";
    public string Message {get;set;}="";
    public InfoBarSeverity Severity {get;set;}
    public bool IsOpen{get=>Visibility==Visibility.Visible;set{Visibility=value?Visibility.Visible:Visibility.Collapsed;text.Text=(Title.Length>0?Title+"\n":"")+Message;}}
    public MessagePanel(){Content=new Border(){Padding=new Thickness(14),CornerRadius=new CornerRadius(8),BorderThickness=new Thickness(1),BorderBrush=new SolidColorBrush(Color.FromArgb(90,128,128,128)),Child=text};Visibility=Visibility.Collapsed;}
}
