using System.ComponentModel;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32.SafeHandles;

namespace UUDisplaySwitch;

internal readonly record struct BackgroundResult(uint ExitCode, string Output, string Error);

// The core is a GUI image so elevated/device operations never open a console.
// Read-only polling must explicitly opt out of Windows' GUI startup cursor.
internal static class BackgroundProcess
{
    const uint StartfUseStdHandles = 0x100, StartfForceOffFeedback = 0x80;
    const uint ExtendedStartupInfoPresent = 0x80000, CreateNoWindow = 0x08000000;
    const uint WaitTimeout = 258, WaitFailed = 0xffffffff;

    internal static async Task<BackgroundResult> RunAsync(string executable, string arguments, TimeSpan timeout)
    {
        if (!Path.IsPathFullyQualified(executable) || executable.Contains('"') || executable.Contains('\0'))
            throw new ArgumentException("An absolute executable path is required.", nameof(executable));
        if (arguments.Contains('\0')) throw new ArgumentException("Invalid arguments.", nameof(arguments));
        if (timeout.TotalMilliseconds < 1 || timeout.TotalMilliseconds >= uint.MaxValue)
            throw new ArgumentOutOfRangeException(nameof(timeout));

        using var outputPipe = new AnonymousPipeServerStream(PipeDirection.In, HandleInheritability.Inheritable);
        using var errorPipe = new AnonymousPipeServerStream(PipeDirection.In, HandleInheritability.Inheritable);
        var security = new SecurityAttributes { Length = Marshal.SizeOf<SecurityAttributes>(), InheritHandle = true };
        using var input = CreateFileW("NUL", 0x80000000, 3, ref security, 3, 0, IntPtr.Zero);
        if (input.IsInvalid) throw Failure("Open background process input");

        ProcessInformation processInfo;
        using (var attributes = new HandleList(input.DangerousGetHandle(), outputPipe.ClientSafePipeHandle.DangerousGetHandle(), errorPipe.ClientSafePipeHandle.DangerousGetHandle()))
        {
            var startup = new StartupInfoEx
            {
                Info = new StartupInfo
                {
                    Size = Marshal.SizeOf<StartupInfoEx>(),
                    Flags = StartfUseStdHandles | StartfForceOffFeedback,
                    StdInput = input.DangerousGetHandle(),
                    StdOutput = outputPipe.ClientSafePipeHandle.DangerousGetHandle(),
                    StdError = errorPipe.ClientSafePipeHandle.DangerousGetHandle()
                },
                AttributeList = attributes.Pointer
            };
            var command = new StringBuilder('"' + executable + "\" " + arguments);
            if (!CreateProcessW(executable, command, IntPtr.Zero, IntPtr.Zero, true,
                    ExtendedStartupInfoPresent | CreateNoWindow, IntPtr.Zero, Path.GetDirectoryName(executable), ref startup, out processInfo))
                throw Failure("Start background process");
        }

        using var process = new SafeKernelHandle(processInfo.Process);
        using var thread = new SafeKernelHandle(processInfo.Thread);
        thread.Dispose();
        StreamReader? output = null, error = null;
        Task<string> outputTask = Task.FromResult(""), errorTask = Task.FromResult("");
        try
        {
            // Keeping either local write end alive would prevent EOF after child exit.
            outputPipe.DisposeLocalCopyOfClientHandle();
            errorPipe.DisposeLocalCopyOfClientHandle();
            output = new StreamReader(outputPipe, Encoding.UTF8);
            error = new StreamReader(errorPipe, Encoding.UTF8);
            outputTask = output.ReadToEndAsync();
            errorTask = error.ReadToEndAsync();
            var waited = await Task.Run(() =>
            {
                uint result = WaitForSingleObject(process, (uint)timeout.TotalMilliseconds);
                return (Result: result, Error: Marshal.GetLastWin32Error());
            }).ConfigureAwait(false);
            if (waited.Result == WaitTimeout) throw new TimeoutException($"Background operation {arguments} timed out.");
            if (waited.Result == WaitFailed) throw new Win32Exception(waited.Error, "Wait for background process");
            if (waited.Result != 0) throw new IOException($"Unexpected background wait result: {waited.Result}.");
            if (!GetExitCodeProcess(process, out uint exitCode)) throw Failure("Read background exit code");
            await Task.WhenAll(outputTask, errorTask).WaitAsync(TimeSpan.FromSeconds(5)).ConfigureAwait(false);
            return new BackgroundResult(exitCode, outputTask.Result, errorTask.Result);
        }
        catch (Exception operationError)
        {
            try
            {
                // Also reclaim the child on wait/stream/setup failure, not only timeout.
                // Terminate only the handle we created, never a looked-up PID.
                if (WaitForSingleObject(process, 0) != 0)
                {
                    if (!TerminateProcess(process, 1))
                    {
                        var failure = Failure("Terminate failed background process");
                        if (WaitForSingleObject(process, 0) != 0) throw failure;
                    }
                    uint stopped = await Task.Run(() => WaitForSingleObject(process, 5000)).ConfigureAwait(false);
                    if (stopped != 0) throw new IOException("Failed background process did not terminate.");
                }
            }
            catch (Exception cleanupError) { throw new AggregateException("Background operation and cleanup failed.", operationError, cleanupError); }
            throw;
        }
        finally
        {
            // Observe read failures even if the wait failed before they completed.
            // Normal output errors still propagate through the awaited reads above.
            ObserveFault(outputTask); ObserveFault(errorTask);
            output?.Dispose(); error?.Dispose();
        }
    }

    static void ObserveFault(Task task) => _ = task.ContinueWith(static failed => { _ = failed.Exception; },
        CancellationToken.None, TaskContinuationOptions.OnlyOnFaulted | TaskContinuationOptions.ExecuteSynchronously, TaskScheduler.Default);

    static Win32Exception Failure(string operation) => new(Marshal.GetLastWin32Error(), operation);

    sealed class HandleList : IDisposable
    {
        public IntPtr Pointer { get; private set; }
        IntPtr values;
        bool initialized;
        public HandleList(params IntPtr[] handles)
        {
            nuint bytes = 0;
            InitializeProcThreadAttributeList(IntPtr.Zero, 1, 0, ref bytes);
            if (bytes == 0) throw Failure("Size process handle list");
            try
            {
                Pointer = Marshal.AllocHGlobal(checked((nint)bytes));
                if (!InitializeProcThreadAttributeList(Pointer, 1, 0, ref bytes)) throw Failure("Initialize process handle list");
                initialized = true;
                values = Marshal.AllocHGlobal(handles.Length * IntPtr.Size);
                Marshal.Copy(handles, 0, values, handles.Length);
                if (!UpdateProcThreadAttribute(Pointer, 0, (nuint)0x20002, values, (nuint)(handles.Length * IntPtr.Size), IntPtr.Zero, IntPtr.Zero))
                    throw Failure("Restrict inherited process handles");
            }
            catch { Dispose(); throw; }
        }
        public void Dispose()
        {
            if (initialized) { DeleteProcThreadAttributeList(Pointer); initialized = false; }
            if (values != IntPtr.Zero) { Marshal.FreeHGlobal(values); values = IntPtr.Zero; }
            if (Pointer != IntPtr.Zero) { Marshal.FreeHGlobal(Pointer); Pointer = IntPtr.Zero; }
        }
    }

    sealed class SafeKernelHandle : SafeHandleZeroOrMinusOneIsInvalid
    {
        // All constructors are internal to this launcher; no PID-based ownership.
        public SafeKernelHandle(IntPtr value) : base(true) { SetHandle(value); }
        protected override bool ReleaseHandle() => CloseHandle(handle);
    }

    [StructLayout(LayoutKind.Sequential)]
    struct SecurityAttributes { public int Length; public IntPtr Descriptor; [MarshalAs(UnmanagedType.Bool)] public bool InheritHandle; }
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct StartupInfo
    {
        public int Size;
        public IntPtr Reserved, Desktop, Title;
        public uint X, Y, XSize, YSize, XChars, YChars, Fill, Flags;
        public ushort ShowWindow, ReservedBytes;
        public IntPtr ReservedData, StdInput, StdOutput, StdError;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct StartupInfoEx { public StartupInfo Info; public IntPtr AttributeList; }
    [StructLayout(LayoutKind.Sequential)]
    struct ProcessInformation { public IntPtr Process, Thread; public uint ProcessId, ThreadId; }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    static extern bool CreateProcessW(string application, StringBuilder command, IntPtr processAttributes, IntPtr threadAttributes,
        [MarshalAs(UnmanagedType.Bool)] bool inheritHandles, uint flags, IntPtr environment, string? directory, ref StartupInfoEx startup, out ProcessInformation process);
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern SafeFileHandle CreateFileW(string name, uint access, uint sharing, ref SecurityAttributes attributes, uint creation, uint flags, IntPtr template);
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    static extern bool InitializeProcThreadAttributeList(IntPtr list, int count, uint flags, ref nuint size);
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    static extern bool UpdateProcThreadAttribute(IntPtr list, uint flags, nuint attribute, IntPtr value, nuint size, IntPtr previous, IntPtr returned);
    [DllImport("kernel32.dll")] static extern void DeleteProcThreadAttributeList(IntPtr list);
    [DllImport("kernel32.dll", SetLastError = true)] static extern uint WaitForSingleObject(SafeKernelHandle handle, uint milliseconds);
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)] static extern bool GetExitCodeProcess(SafeKernelHandle process, out uint exitCode);
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)] static extern bool TerminateProcess(SafeKernelHandle process, uint exitCode);
    [DllImport("kernel32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)] static extern bool CloseHandle(IntPtr handle);
}
