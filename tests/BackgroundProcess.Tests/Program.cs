using System.Diagnostics;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text.Json;
using UUDisplaySwitch;

string fixture = Path.GetFullPath(args[0]);
var passed = new List<string>();
void Check(bool value, string name) { if (!value) throw new Exception(name); passed.Add(name); }
Task<BackgroundResult> Run(string command, int milliseconds = 5000) => BackgroundProcess.RunAsync(fixture, command, TimeSpan.FromMilliseconds(milliseconds));

var normal = await Run("normal");
Check(normal.ExitCode == 0 && normal.Output == "状态正常 · ✓" && normal.Error == "诊断消息", "GUI startup flags, UTF-8 stdout/stderr, NUL stdin");
var large = await Run("large");
Check(large.ExitCode == 7 && large.Output == new string('中',262144) && large.Error == new string('錯',262144), "large parallel streams and nonzero exit code");
using (var unrelated = new EventWaitHandle(false, EventResetMode.ManualReset))
{
    var handle = unrelated.SafeWaitHandle.DangerousGetHandle();
    if (!Native.SetHandleInformation(handle, 1, 1)) throw new Win32Exception();
    try { var result = await Run("inherit " + handle.ToInt64()); Check(result.ExitCode == 0 && result.Output == "isolated", "only explicit standard handles inherited"); }
    finally { Native.SetHandleInformation(handle, 1, 0); }
}
try { await BackgroundProcess.RunAsync(fixture + ".missing", "normal", TimeSpan.FromSeconds(1)); throw new Exception("missing executable was accepted"); }
catch (Win32Exception) { passed.Add("launch failure is reported"); }
var elapsed = Stopwatch.StartNew();
try { await Run("hang", 250); throw new Exception("timeout was ignored"); }
catch (TimeoutException) { Check(elapsed.Elapsed < TimeSpan.FromSeconds(6), "timeout terminates child within bound"); }
var leftovers = Process.GetProcessesByName("BackgroundProcess.Fixture");
try { Check(leftovers.Length == 0, "no child remains after timeout"); }
finally { foreach (var process in leftovers) process.Dispose(); }

// Warm the runtime's asynchronous pipe readers and thread-pool wait resources.
for (int i=0;i<48;i++) await Run("normal");
using var current = Process.GetCurrentProcess(); current.Refresh(); int handlesBefore=current.HandleCount;
var handleSamples = new List<int> { handlesBefore };
for (int i=0;i<40;i++) { var result=await Run("normal"); if(result.ExitCode!=0)throw new Exception("repeated query failed"); current.Refresh(); handleSamples.Add(current.HandleCount); }
current.Refresh(); int handlesAfter=current.HandleCount;
Console.WriteLine(JsonSerializer.Serialize(new { handlesBefore, handlesAfter, handleSamples }));
Check(handlesAfter <= handlesBefore+2, "repeated launches do not accumulate handles");
if (args.Length > 1)
{
    var status = await BackgroundProcess.RunAsync(Path.GetFullPath(args[1]), "--status", TimeSpan.FromSeconds(12));
    using var json = JsonDocument.Parse(status.Output);
    Check(json.RootElement.GetProperty("status").GetString() is "enabled" or "disabled", "real Go core returns structured device state");
}
Console.WriteLine(JsonSerializer.Serialize(new { passed, handlesBefore, handlesAfter },new JsonSerializerOptions {WriteIndented=true}));

static class Native
{
    [DllImport("kernel32.dll", SetLastError=true)] [return: MarshalAs(UnmanagedType.Bool)]
    internal static extern bool SetHandleInformation(IntPtr handle, uint mask, uint flags);
}
