using System.Runtime.InteropServices;
using System.Text;

Console.OutputEncoding = Encoding.UTF8;
var info = Marshal.AllocHGlobal(104);
try
{
    Native.GetStartupInfoW(info);
    uint flags = (uint)Marshal.ReadInt32(info, 60); // STARTUPINFOW x64 dwFlags.
    if ((flags & 0x180) != 0x180) { Console.Error.Write("missing startup flags"); return 20; }
    if (Console.In.ReadToEnd() != "") { Console.Error.Write("stdin was not NUL"); return 21; }
}
finally { Marshal.FreeHGlobal(info); }
if (args[0] == "hang") { Thread.Sleep(60000); return 22; }
if (args[0] == "inherit")
{
    if (Native.GetHandleInformation((IntPtr)long.Parse(args[1]), out _)) { Console.Error.Write("unrelated handle inherited"); return 23; }
    Console.Write("isolated"); return 0;
}
if (args[0] == "large")
{
    Console.Write(new string('中', 262144)); Console.Error.Write(new string('錯', 262144)); return 7;
}
Console.Write("状态正常 · ✓"); Console.Error.Write("诊断消息"); return 0;

static class Native
{
    [DllImport("kernel32.dll")] internal static extern void GetStartupInfoW(IntPtr info);
    [DllImport("kernel32.dll", SetLastError = true)] [return: MarshalAs(UnmanagedType.Bool)]
    internal static extern bool GetHandleInformation(IntPtr handle, out uint flags);
}
