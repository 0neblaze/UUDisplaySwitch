using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Security.AccessControl;
using System.Text.Json;
using System.Xml.Linq;

namespace UUDisplaySwitch;

public static class StartupManager
{
    const string Source = "UUDisplaySwitch.Startup.v1";
    const int ValidateOnly = 1, CreateOrUpdate = 6, InteractiveToken = 3;
    public static string Sid => WindowsIdentity.GetCurrent().User?.Value ?? throw new IOException("Windows user identity unavailable.");
    public static string TaskName => "UUDisplaySwitch-" + Sid;
    public static string ProtectedCore => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), "UUDisplaySwitch", Sid, "UUDisplayCore.exe");

    public static string CreateXml(string sid, string executable)
    {
        XNamespace n = "http://schemas.microsoft.com/windows/2004/02/mit/task";
        XElement E(string name, object value) => new(n + name, value);
        return new XDocument(new XElement(n + "Task", new XAttribute("version", "1.4"),
            E("RegistrationInfo", new[] { E("Description", "UU virtual display: monitor main-process exit after Windows sign-in."), E("Source", Source) }),
            E("Triggers", E("LogonTrigger", new[] { E("Enabled", "true"), E("UserId", sid), E("Delay", "PT10S") })),
            E("Principals", new XElement(n + "Principal", new XAttribute("id", "Owner"), E("UserId", sid), E("LogonType", "InteractiveToken"), E("RunLevel", "HighestAvailable"))),
            E("Settings", new[] { E("MultipleInstancesPolicy", "IgnoreNew"), E("DisallowStartIfOnBatteries", "false"), E("StopIfGoingOnBatteries", "false"), E("AllowHardTerminate", "true"), E("StartWhenAvailable", "true"), E("RunOnlyIfNetworkAvailable", "false"), E("AllowStartOnDemand", "true"), E("Enabled", "true"), E("Hidden", "false"), E("RunOnlyIfIdle", "false"), E("WakeToRun", "false"), E("ExecutionTimeLimit", "PT0S"), E("Priority", "7") }),
            new XElement(n + "Actions", new XAttribute("Context", "Owner"), E("Exec", new[] { E("Command", executable), E("Arguments", "--watch"), E("WorkingDirectory", Path.GetDirectoryName(executable)!) })))).ToString();
    }

    static dynamic Connect()
    {
        dynamic service = Activator.CreateInstance(Type.GetTypeFromProgID("Schedule.Service", true)!)!;
        service.Connect();
        return service;
    }

    static dynamic? Find(dynamic folder)
    {
        try { return folder.GetTask(TaskName); }
        catch (Exception e) when (e.HResult == unchecked((int)0x80070002)) { return null; }
    }

    static void VerifyOwned(string xml)
    {
        var doc = XDocument.Parse(xml);
        XNamespace n = "http://schemas.microsoft.com/windows/2004/02/mit/task";
        var actions = doc.Root!.Element(n + "Actions")!;
        var principal = doc.Root.Element(n + "Principals")?.Element(n + "Principal");
        if ((string?)doc.Root.Element(n + "RegistrationInfo")?.Element(n + "Source") != Source ||
            (string?)principal?.Element(n + "UserId") != Sid ||
            (string?)principal?.Element(n + "LogonType") != "InteractiveToken" ||
            (string?)principal?.Element(n + "RunLevel") != "HighestAvailable" ||
            actions.Elements().Count() != 1 ||
            !string.Equals((string?)actions.Element(n + "Exec")?.Element(n + "Command"), ProtectedCore, StringComparison.OrdinalIgnoreCase) ||
            (string?)actions.Element(n + "Exec")?.Element(n + "Arguments") != "--watch")
            throw new IOException("Existing startup task has an unexpected owner or action; it was not modified.");
    }

    public static bool Read()
    {
        dynamic service = Connect();
        try
        {
            dynamic? task = Find(service.GetFolder("\\"));
            if (task == null) return false;
            VerifyOwned((string)task.Xml);
            return (bool)task.Enabled;
        }
        finally { Marshal.FinalReleaseComObject(service); }
    }

    public static void Configure(bool enable, string expectedSid)
    {
        if (App.ReadOnly) throw new InvalidOperationException("Read-only mode rejects startup changes.");
        if (Sid != expectedSid) throw new InvalidOperationException("Authorize with the same Windows account that opened this application.");
        if (!new WindowsPrincipal(WindowsIdentity.GetCurrent()).IsInRole(WindowsBuiltInRole.Administrator))
            throw new UnauthorizedAccessException("Administrator approval is required to configure background startup.");
        dynamic service = Connect();
        try
        {
            dynamic folder = service.GetFolder("\\");
            dynamic? current = Find(folder);
            if (current != null) VerifyOwned((string)current.Xml);
            if (!enable) { if (current != null) folder.DeleteTask(TaskName, 0); return; }

            // A highest-privilege task must not execute a binary from a user-writable AppData folder.
            Directory.CreateDirectory(Path.GetDirectoryName(ProtectedCore)!);
            Protect(Path.GetDirectoryName(ProtectedCore)!, true);
            bool same = File.Exists(ProtectedCore) && Hash(Core.Exe).SequenceEqual(Hash(ProtectedCore));
            if (!same) File.Copy(Core.Exe, ProtectedCore, true);
            if (!Hash(Core.Exe).SequenceEqual(Hash(ProtectedCore))) throw new IOException("Background component copy verification failed.");
            Protect(ProtectedCore, false);
            folder.RegisterTask(TaskName, CreateXml(Sid, ProtectedCore), CreateOrUpdate, Sid, null, InteractiveToken, null);
            if (!Read()) throw new IOException("Startup registration could not be confirmed.");
        }
        finally { Marshal.FinalReleaseComObject(service); }
    }

    static byte[] Hash(string path) { using var file = File.OpenRead(path); return SHA256.HashData(file); }

    static void Protect(string path, bool directory)
    {
        FileSystemSecurity acl = directory ? new DirectorySecurity() : new FileSecurity();
        var admins = new SecurityIdentifier(WellKnownSidType.BuiltinAdministratorsSid, null);
        acl.SetOwner(admins);
        acl.SetAccessRuleProtection(true, false);
        var inherit = directory ? InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit : InheritanceFlags.None;
        acl.AddAccessRule(new FileSystemAccessRule(admins, FileSystemRights.FullControl, inherit, PropagationFlags.None, AccessControlType.Allow));
        acl.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null), FileSystemRights.FullControl, inherit, PropagationFlags.None, AccessControlType.Allow));
        acl.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(Sid), FileSystemRights.ReadAndExecute, inherit, PropagationFlags.None, AccessControlType.Allow));
        if (directory) new DirectoryInfo(path).SetAccessControl((DirectorySecurity)acl);
        else new FileInfo(path).SetAccessControl((FileSecurity)acl);
    }

    public static async Task Change(bool enable)
    {
        if (App.ReadOnly) throw new InvalidOperationException("Read-only mode rejects startup changes.");
        string request = Guid.NewGuid().ToString("N");
        string resultPath = Path.Combine(Prefs.DataDir, "startup-" + request + ".json");
        var info = new ProcessStartInfo(Environment.ProcessPath!, $"--startup {(enable ? "enable" : "disable")} --owner {Sid} --request {request}")
        { UseShellExecute = true, Verb = "runas", WindowStyle = ProcessWindowStyle.Hidden };
        try
        {
            using var process = Process.Start(info) ?? throw new IOException("Startup configuration helper did not start.");
            await process.WaitForExitAsync();
            if (!File.Exists(resultPath)) throw new IOException($"Startup helper returned {process.ExitCode} without a result.");
            using var result = JsonDocument.Parse(File.ReadAllText(resultPath));
            if (process.ExitCode != 0 || !result.RootElement.GetProperty("ok").GetBoolean())
                throw new IOException(result.RootElement.GetProperty("message").GetString());
            if (await Task.Run(Read) != enable) throw new IOException("Startup state did not match the requested state.");
        }
        finally { if (File.Exists(resultPath)) File.Delete(resultPath); }
    }

    public static int RunCommand(string[] args)
    {
        if (App.ReadOnly) return 13;
        string Value(string key) { int i = Array.IndexOf(args, key); return i >= 0 && i + 1 < args.Length ? args[i + 1] : ""; }
        string request = Value("--request");
        if (!Guid.TryParseExact(request, "N", out _)) return 14;
        bool ok = false; string message = "";
        try
        {
            string mode = Value("--startup");
            if (mode != "enable" && mode != "disable") throw new ArgumentException("Invalid startup mode.");
            Configure(mode == "enable", Value("--owner"));
            ok = true;
        }
        catch (Exception e) { message = e.Message; }
        Directory.CreateDirectory(Prefs.DataDir);
        File.WriteAllText(Path.Combine(Prefs.DataDir, "startup-" + request + ".json"), JsonSerializer.Serialize(new { ok, message }));
        return ok ? 0 : 10;
    }

    public static object ValidatePlan()
    {
        bool before = Read();
        string xml = CreateXml(Sid, ProtectedCore);
        VerifyOwned(xml);
        int rejectedPlans = 0;
        foreach (var invalid in new[] { xml.Replace("--watch", "--apply disable"), xml.Replace(Source, "OtherApp"), xml.Replace("HighestAvailable", "LeastPrivilege") })
        {
            try { VerifyOwned(invalid); }
            catch (IOException) { rejectedPlans++; }
        }
        if (rejectedPlans != 3) throw new IOException("Invalid startup plan was accepted.");
        var doc = XDocument.Parse(xml);
        XNamespace n = "http://schemas.microsoft.com/windows/2004/02/mit/task";
        if (doc.Descendants(n + "LogonTrigger").Count() != 1 || doc.Descendants(n + "RegistrationTrigger").Any() ||
            doc.Descendants(n + "ExecutionTimeLimit").Single().Value != "PT0S" ||
            doc.Descendants(n + "DisallowStartIfOnBatteries").Single().Value != "false" ||
            doc.Descendants(n + "RunLevel").Single().Value != "HighestAvailable") throw new IOException("Startup plan invariant failed.");
        dynamic service = Connect();
        try { service.GetFolder("\\").RegisterTask(TaskName, xml, ValidateOnly, Sid, null, InteractiveToken, null); }
        finally { Marshal.FinalReleaseComObject(service); }
        if (Read() != before) throw new IOException("Read-only validation unexpectedly changed startup state.");
        return new { schemaValid = true, rejectedPlans, taskRegisteredByTest = false, startupBefore = before, startupAfter = Read(), protectedExecutable = ProtectedCore };
    }
}
