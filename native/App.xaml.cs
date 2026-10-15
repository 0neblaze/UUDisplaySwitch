using Microsoft.UI.Xaml;

namespace UUDisplaySwitch;
public partial class App : Application
{
    public static bool ReadOnly => Environment.GetCommandLineArgs().Contains("--read-only");
    public static bool ValidateMonitor => ReadOnly && Environment.GetCommandLineArgs().Contains("--validate-monitor-ui");
    public static bool Validate => Environment.GetCommandLineArgs().Contains("--validate-ui") || ValidateMonitor;
    public static bool ValidateTray => ReadOnly && Environment.GetCommandLineArgs().Contains("--validate-tray");
    public static Window? WindowInstance;
    public App() { InitializeComponent(); UnhandledException += (_,e) => { Directory.CreateDirectory(Prefs.DataDir); File.AppendAllText(Path.Combine(Prefs.DataDir,"native-error.log"),DateTimeOffset.Now+" "+e.Exception+Environment.NewLine); }; DebugSettings.XamlResourceReferenceFailed+=(_,e)=>Trace("Resource: "+e.Message); }
    public static void Trace(string s){if(!ReadOnly)return;Directory.CreateDirectory(Prefs.DataDir);File.AppendAllText(Path.Combine(Prefs.DataDir,"native-trace.log"),DateTimeOffset.Now+" "+s+Environment.NewLine);}
    public static void Record(string action,object detail){try{Directory.CreateDirectory(Prefs.DataDir);string path=Path.Combine(Prefs.DataDir,"native-events.jsonl");if(File.Exists(path)&&new FileInfo(path).Length>262144)File.Move(path,path+".previous",true);File.AppendAllText(path,System.Text.Json.JsonSerializer.Serialize(new{time=DateTimeOffset.Now,action,detail})+Environment.NewLine);}catch(Exception e){System.Diagnostics.Trace.TraceError("Application event log unavailable: "+e.Message);}}
    protected override void OnLaunched(LaunchActivatedEventArgs args) { var command=Environment.GetCommandLineArgs();if(command.Contains("--startup")){Environment.ExitCode=StartupManager.RunCommand(command);Exit();return;} WindowInstance=new MainWindow();WindowInstance.Activate(); }
}
