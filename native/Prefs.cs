using System.Text.Json;
using System.Text.Json.Serialization;
using System.Diagnostics;

namespace UUDisplaySwitch;
public sealed class Prefs
{
    public static string DataDir => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"UUDisplaySwitch");
    public static string FilePath => Path.Combine(DataDir,"settings.json");
    [JsonPropertyName("theme")] public string Theme {get;set;}="system";
    [JsonPropertyName("language")] public string Language {get;set;}="zh-CN";
    [JsonPropertyName("backdrop")] public string Backdrop {get;set;}="none";
    [JsonPropertyName("autoDisable")] public bool AutoDisable {get;set;}
    [JsonPropertyName("autoExit")] public bool AutoExit {get;set;}
    [JsonIgnore] public bool MonitorEnabled => AutoDisable || AutoExit;
    public static Prefs Load(){if(!File.Exists(FilePath))return new();var p=JsonSerializer.Deserialize<Prefs>(File.ReadAllText(FilePath))??throw new IOException("Settings file is empty.");if(!new[]{"system","light","dark"}.Contains(p.Theme)||!new[]{"zh-CN","zh-TW","en"}.Contains(p.Language)||!new[]{"none","mica","mica-alt"}.Contains(p.Backdrop))throw new IOException("Invalid settings value.");return p;}
    public void Save(){Directory.CreateDirectory(DataDir);string tmp=FilePath+"."+Guid.NewGuid().ToString("N")+".tmp";try{File.WriteAllText(tmp,JsonSerializer.Serialize(this));File.Move(tmp,FilePath,true);}finally{if(File.Exists(tmp))File.Delete(tmp);}}
}
public sealed class CoreState
{
    [JsonPropertyName("status")] public string Status {get;set;}="error";
    [JsonPropertyName("problem")] public uint Problem {get;set;}
    [JsonPropertyName("message")] public string Message {get;set;}="";
    [JsonPropertyName("gameRunning")] public bool GameRunning {get;set;}
    [JsonPropertyName("uuRunning")] public bool UuRunning {get;set;}
    [JsonPropertyName("uuStateKnown")] public bool UuStateKnown {get;set;}
    [JsonPropertyName("uuUpdating")] public bool UuUpdating {get;set;}
    [JsonPropertyName("uuStateError")] public string UuStateError {get;set;}="";
    [JsonPropertyName("watcherActive")] public bool WatcherActive {get;set;}
    [JsonPropertyName("watcherStatus")] public string WatcherStatus {get;set;}="stopped";
    [JsonPropertyName("watcherError")] public string WatcherError {get;set;}="";
    [JsonPropertyName("uuExitAt")] public long UuExitAt {get;set;}
}
public static class Core
{
    public static string Exe => Path.Combine(AppContext.BaseDirectory,"UUDisplayCore.exe");
    public static async Task<CoreState> Status(){
        var result=await BackgroundProcess.RunAsync(Exe,"--status",TimeSpan.FromSeconds(12));
        if(string.IsNullOrWhiteSpace(result.Output))throw new IOException($"Device status query failed ({result.ExitCode}). {result.Error}");
        // Device errors also have structured status output for the UI to display.
        return JsonSerializer.Deserialize<CoreState>(result.Output)??throw new IOException("Empty device response.");
    }
    public static async Task Switch(string mode){if(App.ReadOnly)throw new InvalidOperationException("Read-only mode");if(mode!="enable"&&mode!="disable")throw new ArgumentException("Invalid mode");var si=new ProcessStartInfo(Exe,"--apply "+mode){UseShellExecute=true,Verb="runas",WindowStyle=ProcessWindowStyle.Hidden};using var p=Process.Start(si)??throw new IOException("Could not start device operation.");await p.WaitForExitAsync();if(p.ExitCode!=0)throw new IOException("Device operation failed ("+p.ExitCode+").");}
    public static async Task<CoreState> StartWatcher(){
        if(App.ReadOnly)throw new InvalidOperationException("Read-only mode");
        var current=await Status();if(current.WatcherActive)return current;
        using var process=Process.Start(new ProcessStartInfo(Exe,"--watch"){UseShellExecute=true,Verb="runas",WindowStyle=ProcessWindowStyle.Hidden})??throw new IOException("Monitor did not start.");
        long started=DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();var elapsed=Stopwatch.StartNew();
        return await AwaitMonitor(Status,()=>process.HasExited?process.ExitCode:null,()=>Task.Delay(200),()=>elapsed.Elapsed>TimeSpan.FromSeconds(10),started);
    }
    internal static async Task<CoreState> AwaitMonitor(Func<Task<CoreState>> read,Func<int?> exitCode,Func<Task> delay,Func<bool> expired,long started){
        while(true){
            var state=await read();if(state.WatcherActive)return state;
            if(state.UuStateKnown&&!state.UuRunning&&state.UuExitAt>started)return state;
            var code=exitCode();
            // A concurrent launch can own the mutex before publishing its first heartbeat.
            if(code.HasValue&&code.Value!=0)throw new IOException($"Monitor exited before it became ready (code {code}). {state.WatcherError}");
            if(expired())throw new TimeoutException("Monitor did not become ready. "+state.WatcherError);
            await delay();
        }
    }
    internal static async Task ValidateMonitorStartup(){
        int polls=0;
        var state=await AwaitMonitor(()=>Task.FromResult(new CoreState{WatcherActive=++polls==4}),()=>null,()=>Task.CompletedTask,()=>false,100);
        if(!state.WatcherActive||polls!=4)throw new InvalidOperationException("Delayed monitor startup failed");
        polls=0;
        state=await AwaitMonitor(()=>Task.FromResult(new CoreState{WatcherActive=++polls==3}),()=>0,()=>Task.CompletedTask,()=>false,100);
        if(!state.WatcherActive||polls!=3)throw new InvalidOperationException("Concurrent monitor startup failed");
        try{await AwaitMonitor(()=>Task.FromResult(new CoreState()),()=>10,()=>Task.CompletedTask,()=>false,100);throw new InvalidOperationException("Monitor failure was ignored");}catch(IOException){}
        try{await AwaitMonitor(()=>Task.FromResult(new CoreState()),()=>null,()=>Task.CompletedTask,()=>true,100);throw new InvalidOperationException("Monitor timeout was ignored");}catch(TimeoutException){}
    }
    public static async Task StopWatcher(){
        if(App.ReadOnly)throw new InvalidOperationException("Read-only mode");
        var result=await BackgroundProcess.RunAsync(Exe,"--stop-watch",TimeSpan.FromSeconds(15));
        if(result.ExitCode!=0)throw new IOException($"Monitor stop failed ({result.ExitCode}): {result.Error}");
    }
}
