using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.ExceptionServices;
using System.Runtime.InteropServices;

namespace UUDisplaySwitch;

public readonly record struct TrayIconLabels(string ToolTip, string ShowWindow, string ExitApplication);

/// <summary>Owns one native notification-area icon attached to an existing top-level window.</summary>
public sealed class TrayIcon : IDisposable
{
    private const uint NimAdd = 0x00000000;
    private const uint NimModify = 0x00000001;
    private const uint NimDelete = 0x00000002;
    private const uint NimSetFocus = 0x00000003;
    private const uint NimSetVersion = 0x00000004;
    private const uint NotifyIconVersion4 = 4;
    private const uint NifMessage = 0x00000001;
    private const uint NifIcon = 0x00000002;
    private const uint NifTip = 0x00000004;
    private const uint NifShowTip = 0x00000080;
    private const uint ImageIcon = 1;
    private const uint LrDefaultSize = 0x00000040;
    private const uint LrLoadFromFile = 0x00000010;
    private const uint WmApp = 0x8000;
    private const uint WmContextMenu = 0x007B;
    private const uint WmCommand = 0x0111;
    private const uint WmMeasureItem = 0x002C;
    private const uint WmDrawItem = 0x002B;
    private const uint WmNcCreate = 0x0081;
    private const uint WmNcDestroy = 0x0082;
    private const uint WmLButtonDblClk = 0x0203;
    private const uint WmRButtonUp = 0x0205;
    private const uint NinKeySelect = 0x0401;
    private const uint TpmRightButton = 0x0002;
    private const uint MfOwnerDraw = 0x00000100;
    private const uint MiimId = 0x00000002;
    private const uint MiimData = 0x00000020;
    private const uint MiimString = 0x00000040;
    private const uint MiimFType = 0x00000100;
    private const uint MsaaMenuSignature = 0xAA0DF00D;
    private const uint OdTMenu = 1;
    private const uint OdsSelected = 0x0001;
    private const uint WsPopup = 0x80000000;
    private const uint WsExToolWindow = 0x00000080;
    private const uint SwpNoSize = 0x0001;
    private const uint SwpNoZOrder = 0x0004;
    private const uint SwpNoActivate = 0x0010;
    private const uint SpiGetNonClientMetrics = 0x0029;
    private const uint SpiGetHighContrast = 0x0042;
    private const uint HcfHighContrastOn = 0x00000001;
    private const int GwlUserData = -21;
    private const int ColorMenu = 4;
    private const int ColorMenuText = 7;
    private const int ColorHighlight = 13;
    private const int ColorHighlightText = 14;
    private const uint DtSingleLine = 0x00000020;
    private const uint DtVCenter = 0x00000004;
    private const uint DtLeft = 0x00000000;
    private const uint DtNoPrefix = 0x00000800;
    private const int Transparent = 1;
    private const uint ErrorClassAlreadyExists = 1410;
    private const string MenuOwnerClassName = "UUDisplaySwitch.TrayMenuOwner.v1";
    internal const uint MenuShowWindowCommand = 0x6301;
    internal const uint MenuExitCommand = 0x6302;
    private readonly uint _callbackMessage;

    private static int _nextIconId;
    private static int _nextSubclassId;
    private static readonly object MenuOwnerClassLock = new();
    private static readonly MenuOwnerProc MenuOwnerWindowProcCallback = MenuOwnerWindowProc;
    private static bool _menuOwnerClassRegistered;

    private readonly IntPtr _hwnd;
    private readonly uint _iconId;
    private readonly UIntPtr _subclassId;
    private readonly uint _taskbarCreatedMessage;
    private readonly Action _show;
    private readonly Action _exit;
    private readonly Action<Exception> _reportError;
    private readonly SubclassProc _subclassProc;
    private readonly List<(IntPtr Info, IntPtr Text)> _menuAccessibilityData = [];
    private GCHandle _menuOwnerReference;
    private IntPtr _menuOwnerHwnd;
    private IntPtr _menuFont;
    private IntPtr _iconHandle;
    private TrayIconLabels _labels;
    private bool _darkTheme;
    private bool _subclassed;
    private bool _iconAdded;
    private bool _disposed;
    private bool _menuOperationActive;
    private bool _menuOpen;
    private bool _menuCommandDispatched;
    private uint _pendingMenuCommand;

    public TrayIcon(IntPtr hwnd, string iconPath, TrayIconLabels labels, Action show, Action exit, Action<Exception> reportError)
    {
        if (IntPtr.Size != 8)
            throw new PlatformNotSupportedException("The notification icon ABI is configured for 64-bit Windows.");
        if (hwnd == IntPtr.Zero || !IsWindow(hwnd))
            throw new ArgumentException("A valid top-level window handle is required.", nameof(hwnd));

        _hwnd = hwnd;
        _iconId = (uint)((Interlocked.Increment(ref _nextIconId) - 1) % ushort.MaxValue + 1);
        _subclassId = new UIntPtr(unchecked((uint)Interlocked.Increment(ref _nextSubclassId)));
        _show = show ?? throw new ArgumentNullException(nameof(show));
        _exit = exit ?? throw new ArgumentNullException(nameof(exit));
        _reportError = reportError ?? throw new ArgumentNullException(nameof(reportError));
        _labels = ValidateLabels(labels);
        _subclassProc = WindowProc;

        _taskbarCreatedMessage = RegisterWindowMessageW("TaskbarCreated");
        if (_taskbarCreatedMessage == 0)
            throw LastError("RegisterWindowMessage(TaskbarCreated)");
        _callbackMessage = RegisterWindowMessageW("UUDisplaySwitch.TrayCallback.v1");
        if (_callbackMessage == 0) throw LastError("RegisterWindowMessage(TrayCallback)");

        string fullIconPath = Path.GetFullPath(iconPath ?? throw new ArgumentNullException(nameof(iconPath)));
        if (!File.Exists(fullIconPath))
            throw new FileNotFoundException("The tray icon file was not found.", fullIconPath);

        try
        {
            _iconHandle = LoadImageW(IntPtr.Zero, fullIconPath, ImageIcon, 0, 0, LrLoadFromFile | LrDefaultSize);
            if (_iconHandle == IntPtr.Zero)
                throw LastError($"LoadImage({fullIconPath})");

            CreateMenuOwnerWindow();

            if (!SetWindowSubclass(_hwnd, _subclassProc, _subclassId, UIntPtr.Zero))
                throw LastError("SetWindowSubclass");
            _subclassed = true;

            AddIcon();
        }
        catch (Exception initializationError)
        {
            List<Exception> cleanupErrors = Cleanup();
            if (cleanupErrors.Count > 0)
                throw new AggregateException("Tray icon initialization failed and cleanup also reported errors.", [initializationError, .. cleanupErrors]);

            ExceptionDispatchInfo.Capture(initializationError).Throw();
            throw;
        }
    }

    public void UpdateLabels(TrayIconLabels labels)
    {
        ThrowIfDisposed();
        TrayIconLabels validated = ValidateLabels(labels);
        NotifyIconData data = CreateData(validated, NifTip | NifShowTip);
        Notify(NimModify, ref data, "Shell_NotifyIcon(NIM_MODIFY)");
        _labels = validated;
    }

    public void UpdateTheme(bool dark)
    {
        ThrowIfDisposed();
        _darkTheme = dark;
    }

    internal IntPtr MenuOwnerHandle => _menuOwnerHwnd;

    public void Dispose()
    {
        if (_disposed && !_iconAdded && !_subclassed && _iconHandle == IntPtr.Zero &&
            _menuOwnerHwnd == IntPtr.Zero && _menuFont == IntPtr.Zero && !_menuOwnerReference.IsAllocated)
            return;

        List<Exception> errors = Cleanup();
        if (errors.Count == 1)
            ExceptionDispatchInfo.Capture(errors[0]).Throw();
        if (errors.Count > 1)
            throw new AggregateException("One or more tray icon resources could not be released.", errors);
    }

    private void AddIcon()
    {
        NotifyIconData data = CreateData(_labels, NifMessage | NifIcon | NifTip | NifShowTip);
        Notify(NimAdd, ref data, "Shell_NotifyIcon(NIM_ADD)");
        _iconAdded = true;

        data.uVersion = NotifyIconVersion4;
        Notify(NimSetVersion, ref data, "Shell_NotifyIcon(NIM_SETVERSION)");
    }

    private void RestoreIcon()
    {
        _iconAdded = false;
        AddIcon();
    }

    private IntPtr WindowProc(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam, UIntPtr subclassId, UIntPtr refData)
    {
        if (message == 0x111 || message == 0x112)
            App.Trace($"Tray native command {message:X} {wParam.ToUInt64():X} {lParam.ToInt64():X}");
        try
        {
            if (_disposed)
                return DefSubclassProc(hwnd, message, wParam, lParam);

            if (message == _taskbarCreatedMessage)
            {
                RestoreIcon();
                return IntPtr.Zero;
            }

            if (message == WmCommand && lParam == IntPtr.Zero)
            {
                if (HandleMenuCommand(wParam))
                    return IntPtr.Zero;
            }

            if (message == _callbackMessage)
            {
                long callback = lParam.ToInt64();
                uint eventCode = unchecked((ushort)(callback & 0xFFFF));
                uint eventIconId = unchecked((ushort)((callback >> 16) & 0xFFFF));
                if (eventIconId != _iconId)
                    return DefSubclassProc(hwnd, message, wParam, lParam);

                if (eventCode == WmContextMenu || eventCode == WmRButtonUp)
                {
                    App.Trace($"Tray callback event={eventCode:X} menuOpen={_menuOpen} font={_menuFont.ToInt64():X}");
                    ShowContextMenu(wParam);
                    return IntPtr.Zero;
                }

                if (eventCode == WmLButtonDblClk || eventCode == NinKeySelect)
                {
                    _show();
                    return IntPtr.Zero;
                }
            }
        }
        catch (Exception error)
        {
            try
            {
                _reportError(error);
            }
            catch (Exception reportingError)
            {
                Trace.TraceError("Tray callback failed: {0}{1}Error reporter failed: {2}", error, Environment.NewLine, reportingError);
            }

            return IntPtr.Zero;
        }

        return DefSubclassProc(hwnd, message, wParam, lParam);
    }

    private bool HandleMenuCommand(UIntPtr wParam)
    {
        uint command = unchecked((ushort)wParam.ToUInt64());
        if (command != MenuShowWindowCommand && command != MenuExitCommand)
            return false;

        if (_menuOpen)
        {
            if (_pendingMenuCommand == 0)
            {
                _pendingMenuCommand = command;
                Marshal.SetLastPInvokeError(0);
                if (!EndMenu())
                    throw LastError("EndMenu");
            }

            return true;
        }

        if (!_menuCommandDispatched)
        {
            _menuCommandDispatched = true;
            DispatchMenuCommand(command);
        }

        return true;
    }

    private void ShowContextMenu(UIntPtr packedAnchor)
    {
        // A real Shell right-click can deliver both WM_RBUTTONUP and WM_CONTEXTMENU.
        // TrackPopupMenuEx pumps messages, so the second callback can arrive before
        // the first menu returns. Only one operation may own these native resources.
        if (_menuOperationActive)
        {
            App.Trace("Tray duplicate menu request ignored while the current operation owns its resources");
            return;
        }

        _menuOperationActive = true;
        try
        {
            ShowContextMenuCore(packedAnchor);
        }
        finally
        {
            _menuOperationActive = false;
        }
    }

    private void ShowContextMenuCore(UIntPtr packedAnchor)
    {
        App.Trace($"Tray menu enter open={_menuOpen} font={_menuFont.ToInt64():X}");
        IntPtr menu = CreatePopupMenu();
        if (menu == IntPtr.Zero)
            throw LastError("CreatePopupMenu");

        Exception? operationError = null;
        Exception? fontError = null;
        IntPtr ownedFont = IntPtr.Zero;
        try
        {
            if (!InsertOwnerDrawMenuItem(menu, 0, MenuShowWindowCommand, _labels.ShowWindow))
                throw LastError("InsertMenuItem(Show window)");
            if (!InsertOwnerDrawMenuItem(menu, 1, MenuExitCommand, _labels.ExitApplication))
                throw LastError("InsertMenuItem(Exit)");

            long packed = unchecked((long)packedAnchor.ToUInt64());
            int x = unchecked((short)(packed & 0xFFFF));
            int y = unchecked((short)((packed >> 16) & 0xFFFF));
            if (x == short.MinValue && y == short.MinValue)
            {
                if (!GetCursorPos(out Point cursor))
                    throw LastError("GetCursorPos");
                x = cursor.X;
                y = cursor.Y;
            }

            if (!SetWindowPos(_menuOwnerHwnd, IntPtr.Zero, x, y, 0, 0, SwpNoSize | SwpNoZOrder | SwpNoActivate))
                throw LastError("SetWindowPos(tray menu owner)");
            if (_menuFont != IntPtr.Zero)
                throw new InvalidOperationException("The previous tray menu font could not be released.");
            _menuFont = ownedFont = CreateMenuFont(_menuOwnerHwnd);
            _ = SetForegroundWindow(_menuOwnerHwnd);
            _pendingMenuCommand = 0;
            _menuCommandDispatched = false;
            _menuOpen = true;
            Marshal.SetLastPInvokeError(0);
            bool tracked;
            int trackError;
            try
            {
                tracked = TrackPopupMenuEx(menu, TpmRightButton, x, y, _menuOwnerHwnd, IntPtr.Zero);
                trackError = Marshal.GetLastPInvokeError();
            }
            finally
            {
                _menuOpen = false;
            }

            // TrackPopupMenuEx returns false when the user dismisses the menu. Treat that
            // as cancellation unless Windows supplied an actual error code.
            if (!tracked && trackError != 0)
                throw new Win32Exception(trackError, "TrackPopupMenuEx failed.");
            NotifyFocus();
        }
        catch (Exception error)
        {
            operationError = error;
        }
        finally
        {
            App.Trace($"Tray menu cleanup open={_menuOpen} font={_menuFont.ToInt64():X} error={operationError?.Message}");
            _menuOpen = false;
            if (ownedFont != IntPtr.Zero)
            {
                if (DeleteObject(ownedFont))
                    _menuFont = IntPtr.Zero;
                else
                    fontError = LastError("DeleteObject(tray menu font)");
            }
        }

        bool menuDestroyed = DestroyMenu(menu);
        Exception? destroyError = menuDestroyed ? null : LastError("DestroyMenu");
        if (menuDestroyed)
            FreeMenuAccessibilityData();
        uint commandToDispatch = _pendingMenuCommand;
        _pendingMenuCommand = 0;
        _menuCommandDispatched = commandToDispatch != 0;

        Exception? dispatchError = null;
        if (commandToDispatch != 0)
        {
            try
            {
                DispatchMenuCommand(commandToDispatch);
            }
            catch (Exception error)
            {
                dispatchError = error;
            }
        }

        var errors = new List<Exception>(4);
        if (operationError is not null)
            errors.Add(operationError);
        if (fontError is not null)
            errors.Add(fontError);
        if (destroyError is not null)
            errors.Add(destroyError);
        if (dispatchError is not null)
            errors.Add(dispatchError);

        if (errors.Count == 1)
            ExceptionDispatchInfo.Capture(errors[0]).Throw();
        if (errors.Count > 1)
            throw new AggregateException("The tray menu operation, cleanup, or command dispatch failed.", errors);
    }

    private bool InsertOwnerDrawMenuItem(IntPtr menu, uint position, uint command, string label)
    {
        IntPtr text = Marshal.StringToHGlobalUni(label);
        IntPtr infoPointer = IntPtr.Zero;
        bool inserted = false;
        int insertError = 0;
        try
        {
            infoPointer = Marshal.AllocHGlobal(Marshal.SizeOf<MsaaMenuInfo>());
            var accessibilityInfo = new MsaaMenuInfo
            {
                dwMSAASignature = MsaaMenuSignature,
                cchWText = checked((uint)label.Length),
                pszWText = text
            };
            Marshal.StructureToPtr(accessibilityInfo, infoPointer, false);

            var item = new MenuItemInfo
            {
                cbSize = checked((uint)Marshal.SizeOf<MenuItemInfo>()),
                fMask = MiimId | MiimFType | MiimString | MiimData,
                fType = MfOwnerDraw,
                wID = command,
                dwItemData = new UIntPtr(unchecked((ulong)infoPointer.ToInt64())),
                dwTypeData = label,
                cch = checked((uint)label.Length)
            };

            inserted = InsertMenuItemW(menu, position, true, ref item);
            if (inserted)
                _menuAccessibilityData.Add((infoPointer, text));
            else
                insertError = Marshal.GetLastPInvokeError();
            return inserted;
        }
        finally
        {
            if (!inserted)
            {
                if (infoPointer != IntPtr.Zero)
                    Marshal.FreeHGlobal(infoPointer);
                Marshal.FreeHGlobal(text);
                Marshal.SetLastPInvokeError(insertError);
            }
        }
    }

    private void FreeMenuAccessibilityData()
    {
        foreach ((IntPtr info, IntPtr text) in _menuAccessibilityData)
        {
            Marshal.FreeHGlobal(info);
            Marshal.FreeHGlobal(text);
        }
        _menuAccessibilityData.Clear();
    }

    private void CreateMenuOwnerWindow()
    {
        EnsureMenuOwnerClassRegistered();
        IntPtr module = GetModuleHandleW(null);
        if (module == IntPtr.Zero)
            throw LastError("GetModuleHandle");

        _menuOwnerReference = GCHandle.Alloc(this);
        _menuOwnerHwnd = CreateWindowExW(
            WsExToolWindow,
            MenuOwnerClassName,
            string.Empty,
            WsPopup,
            0,
            0,
            0,
            0,
            IntPtr.Zero,
            IntPtr.Zero,
            module,
            GCHandle.ToIntPtr(_menuOwnerReference));

        if (_menuOwnerHwnd == IntPtr.Zero)
        {
            if (_menuOwnerReference.IsAllocated)
                _menuOwnerReference.Free();
            throw LastError("CreateWindowEx(tray menu owner)");
        }
    }

    private static void EnsureMenuOwnerClassRegistered()
    {
        if (_menuOwnerClassRegistered)
            return;

        lock (MenuOwnerClassLock)
        {
            if (_menuOwnerClassRegistered)
                return;

            IntPtr module = GetModuleHandleW(null);
            if (module == IntPtr.Zero)
                throw LastError("GetModuleHandle");

            var windowClass = new WindowClassEx
            {
                cbSize = checked((uint)Marshal.SizeOf<WindowClassEx>()),
                lpfnWndProc = MenuOwnerWindowProcCallback,
                hInstance = module,
                lpszClassName = MenuOwnerClassName
            };

            if (RegisterClassExW(ref windowClass) == 0 && Marshal.GetLastPInvokeError() != ErrorClassAlreadyExists)
                throw LastError("RegisterClassEx(tray menu owner)");

            _menuOwnerClassRegistered = true;
        }
    }

    private static IntPtr MenuOwnerWindowProc(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam)
    {
        IntPtr reference = GetWindowLongPtrW(hwnd, GwlUserData);
        if (message == WmNcCreate)
        {
            try
            {
                CreateStruct create = Marshal.PtrToStructure<CreateStruct>(lParam);
                reference = create.lpCreateParams;
                if (reference == IntPtr.Zero)
                    return IntPtr.Zero;

                Marshal.SetLastPInvokeError(0);
                IntPtr previous = SetWindowLongPtrW(hwnd, GwlUserData, reference);
                if (previous == IntPtr.Zero && Marshal.GetLastPInvokeError() != 0)
                    throw LastError("SetWindowLongPtr(tray menu owner)");
            }
            catch (Exception error)
            {
                ReportMenuOwnerCallbackError(null, error);
                return IntPtr.Zero;
            }
        }

        TrayIcon? tray = null;
        if (reference != IntPtr.Zero)
        {
            try
            {
                tray = GCHandle.FromIntPtr(reference).Target as TrayIcon;
            }
            catch (Exception error)
            {
                ReportMenuOwnerCallbackError(null, error);
            }
        }

        IntPtr result;
        if (tray is null)
        {
            result = DefWindowProcW(hwnd, message, wParam, lParam);
        }
        else
        {
            try
            {
                result = tray.HandleMenuOwnerMessage(hwnd, message, wParam, lParam);
            }
            catch (Exception error)
            {
                ReportMenuOwnerCallbackError(tray, error);
                result = DefWindowProcW(hwnd, message, wParam, lParam);
            }
        }

        if (message == WmNcDestroy)
        {
            try
            {
                Marshal.SetLastPInvokeError(0);
                _ = SetWindowLongPtrW(hwnd, GwlUserData, IntPtr.Zero);
            }
            catch (Exception error)
            {
                ReportMenuOwnerCallbackError(tray, error);
            }

            if (reference != IntPtr.Zero)
            {
                try
                {
                    GCHandle.FromIntPtr(reference).Free();
                    if (tray is not null)
                        tray._menuOwnerReference = default;
                }
                catch (Exception error)
                {
                    ReportMenuOwnerCallbackError(tray, error);
                }
            }

            if (tray is not null && tray._menuOwnerHwnd == hwnd)
                tray._menuOwnerHwnd = IntPtr.Zero;
        }

        return result;
    }

    private IntPtr HandleMenuOwnerMessage(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam)
    {
        if (!_disposed)
        {
            if (message == WmCommand && lParam == IntPtr.Zero && HandleMenuCommand(wParam))
                return IntPtr.Zero;
            if (message == WmMeasureItem && TryMeasureMenuItem(lParam))
                return new IntPtr(1);
            if (message == WmDrawItem && TryDrawMenuItem(lParam))
                return new IntPtr(1);
        }

        return DefWindowProcW(hwnd, message, wParam, lParam);
    }

    private bool TryMeasureMenuItem(IntPtr structurePointer)
    {
        if (structurePointer == IntPtr.Zero)
            return false;

        MeasureItemStruct measure = Marshal.PtrToStructure<MeasureItemStruct>(structurePointer);
        if (measure.CtlType != OdTMenu || GetMenuItemLabel(measure.itemID) is not string label)
            return false;

        uint dpi = GetMenuDpi(_menuOwnerHwnd);
        IntPtr dc = GetDC(_menuOwnerHwnd);
        if (dc == IntPtr.Zero)
            throw LastError("GetDC(tray menu owner)");

        IntPtr previousFont = IntPtr.Zero;
        try
        {
            previousFont = SelectObject(dc, _menuFont);
            if (previousFont == IntPtr.Zero || previousFont == new IntPtr(-1))
                throw LastError("SelectObject(tray menu font)");
            if (!GetTextExtentPoint32W(dc, label, label.Length, out Size extent))
                throw LastError("GetTextExtentPoint32(tray menu item)");

            measure.itemWidth = checked((uint)Math.Max(1, extent.X) + ScaleForDpi(36, dpi));
            measure.itemHeight = Math.Max(ScaleForDpi(32, dpi), checked((uint)Math.Max(1, extent.Y) + ScaleForDpi(12, dpi)));
            Marshal.StructureToPtr(measure, structurePointer, false);
        }
        finally
        {
            if (previousFont != IntPtr.Zero && previousFont != new IntPtr(-1))
                _ = SelectObject(dc, previousFont);
            _ = ReleaseDC(_menuOwnerHwnd, dc);
        }

        return true;
    }

    private bool TryDrawMenuItem(IntPtr structurePointer)
    {
        if (structurePointer == IntPtr.Zero)
            return false;

        DrawItemStruct item = Marshal.PtrToStructure<DrawItemStruct>(structurePointer);
        if (item.CtlType != OdTMenu || GetMenuItemLabel(item.itemID) is not string label)
            return false;

        uint dpi = GetMenuDpi(_menuOwnerHwnd);
        bool highContrast = IsHighContrastEnabled();
        bool selected = (item.itemState & OdsSelected) != 0;
        uint background = highContrast
            ? GetSysColor(selected ? ColorHighlight : ColorMenu)
            : selected
                ? (_darkTheme ? ColorRef(58, 58, 58) : ColorRef(228, 228, 228))
                : (_darkTheme ? ColorRef(32, 32, 32) : ColorRef(250, 250, 250));
        uint foreground = highContrast
            ? GetSysColor(selected ? ColorHighlightText : ColorMenuText)
            : (_darkTheme ? ColorRef(245, 245, 245) : ColorRef(32, 32, 32));

        int savedDc = SaveDC(item.hDC);
        if (savedDc == 0)
            throw LastError("SaveDC(tray menu)");

        try
        {
            IntPtr brush = CreateSolidBrush(background);
            if (brush == IntPtr.Zero)
                throw LastError("CreateSolidBrush(tray menu)");

            try
            {
                if (FillRect(item.hDC, ref item.rcItem, brush) == 0)
                    throw LastError("FillRect(tray menu)");
            }
            finally
            {
                _ = DeleteObject(brush);
            }

            IntPtr previousFont = SelectObject(item.hDC, _menuFont);
            if (previousFont == IntPtr.Zero || previousFont == new IntPtr(-1))
                throw LastError("SelectObject(tray menu font)");

            try
            {
                if (SetBkMode(item.hDC, Transparent) == 0)
                    throw LastError("SetBkMode(tray menu)");
                if (SetTextColor(item.hDC, foreground) == uint.MaxValue)
                    throw LastError("SetTextColor(tray menu)");

                Rect textRect = item.rcItem;
                textRect.Left += checked((int)ScaleForDpi(18, dpi));
                textRect.Right -= checked((int)ScaleForDpi(12, dpi));
                if (DrawTextW(item.hDC, label, -1, ref textRect, DtSingleLine | DtVCenter | DtLeft | DtNoPrefix) == 0)
                    throw LastError("DrawText(tray menu item)");
            }
            finally
            {
                _ = SelectObject(item.hDC, previousFont);
            }
        }
        finally
        {
            _ = RestoreDC(item.hDC, savedDc);
        }

        return true;
    }

    private string? GetMenuItemLabel(uint itemId) => itemId switch
    {
        MenuShowWindowCommand => _labels.ShowWindow,
        MenuExitCommand => _labels.ExitApplication,
        _ => null
    };

    private static IntPtr CreateMenuFont(IntPtr hwnd)
    {
        uint dpi = GetMenuDpi(hwnd);
        var metrics = new NonClientMetrics { cbSize = checked((uint)Marshal.SizeOf<NonClientMetrics>()) };
        if (!SystemParametersInfoForDpi(SpiGetNonClientMetrics, metrics.cbSize, ref metrics, 0, dpi))
            throw LastError("SystemParametersInfoForDpi(NONCLIENTMETRICS)");

        IntPtr font = CreateFontIndirectW(ref metrics.lfMenuFont);
        if (font == IntPtr.Zero)
            throw LastError("CreateFontIndirect(tray menu)");
        return font;
    }

    private static uint GetMenuDpi(IntPtr hwnd)
    {
        uint dpi = GetDpiForWindow(hwnd);
        if (dpi == 0)
            throw LastError("GetDpiForWindow(tray menu owner)");
        return dpi;
    }

    private static uint ScaleForDpi(uint value, uint dpi) => checked((value * dpi + 48) / 96);

    private static bool IsHighContrastEnabled()
    {
        var highContrast = new HighContrast { cbSize = checked((uint)Marshal.SizeOf<HighContrast>()) };
        if (!SystemParametersInfoW(SpiGetHighContrast, highContrast.cbSize, ref highContrast, 0))
            throw LastError("SystemParametersInfo(SPI_GETHIGHCONTRAST)");
        return (highContrast.dwFlags & HcfHighContrastOn) != 0;
    }

    private static uint ColorRef(byte red, byte green, byte blue) => (uint)(red | (green << 8) | (blue << 16));

    private static void ReportMenuOwnerCallbackError(TrayIcon? tray, Exception error)
    {
        if (tray is null)
        {
            Trace.TraceError("Tray menu owner callback failed: {0}", error);
            return;
        }

        try
        {
            tray._reportError(error);
        }
        catch (Exception reportingError)
        {
            Trace.TraceError("Tray menu owner callback failed: {0}{1}Error reporter failed: {2}", error, Environment.NewLine, reportingError);
        }
    }

    internal bool DispatchMenuCommand(uint command) => DispatchMenuCommand(command, _show, _exit);

    internal static bool DispatchMenuCommand(uint command, Action show, Action exit)
    {
        ArgumentNullException.ThrowIfNull(show);
        ArgumentNullException.ThrowIfNull(exit);
        switch (command)
        {
            case MenuShowWindowCommand:
                show();
                return true;
            case MenuExitCommand:
                exit();
                return true;
            default:
                return false;
        }
    }

    private void NotifyFocus()
    {
        NotifyIconData data = CreateData(_labels, 0);
        Notify(NimSetFocus, ref data, "Shell_NotifyIcon(NIM_SETFOCUS)");
    }

    private NotifyIconData CreateData(TrayIconLabels labels, uint flags) => new()
    {
        cbSize = checked((uint)Marshal.SizeOf<NotifyIconData>()),
        hWnd = _hwnd,
        uID = _iconId,
        uFlags = flags,
        uCallbackMessage = _callbackMessage,
        hIcon = _iconHandle,
        szTip = LimitToolTip(labels.ToolTip),
        szInfo = string.Empty,
        szInfoTitle = string.Empty
    };

    private static string LimitToolTip(string value)
    {
        const int maxCharacters = 127;
        if (value.Length <= maxCharacters)
            return value;
        string result = value[..maxCharacters];
        return char.IsHighSurrogate(result[^1]) ? result[..^1] : result;
    }

    private static TrayIconLabels ValidateLabels(TrayIconLabels labels)
    {
        if (string.IsNullOrWhiteSpace(labels.ToolTip))
            throw new ArgumentException("A non-empty tray tooltip is required.", nameof(labels));
        if (string.IsNullOrWhiteSpace(labels.ShowWindow))
            throw new ArgumentException("A non-empty show-window menu label is required.", nameof(labels));
        if (string.IsNullOrWhiteSpace(labels.ExitApplication))
            throw new ArgumentException("A non-empty exit menu label is required.", nameof(labels));
        return labels;
    }

    private void Notify(uint operation, ref NotifyIconData data, string operationName)
    {
        if (!Shell_NotifyIconW(operation, ref data))
            throw LastError(operationName);
    }

    private List<Exception> Cleanup()
    {
        _disposed = true;
        var errors = new List<Exception>();

        if (_iconAdded)
        {
            NotifyIconData data = CreateData(_labels, 0);
            if (!Shell_NotifyIconW(NimDelete, ref data))
                errors.Add(LastError("Shell_NotifyIcon(NIM_DELETE)"));
            else
                _iconAdded = false;
        }

        if (_subclassed)
        {
            if (!RemoveWindowSubclass(_hwnd, _subclassProc, _subclassId))
                errors.Add(LastError("RemoveWindowSubclass"));
            else
                _subclassed = false;
        }

        if (_menuFont != IntPtr.Zero)
        {
            if (!DeleteObject(_menuFont))
                errors.Add(LastError("DeleteObject(tray menu font)"));
            else
                _menuFont = IntPtr.Zero;
        }

        if (_menuOwnerHwnd != IntPtr.Zero)
        {
            if (!DestroyWindow(_menuOwnerHwnd))
                errors.Add(LastError("DestroyWindow(tray menu owner)"));
            else
                _menuOwnerHwnd = IntPtr.Zero;
        }

        if (_iconHandle != IntPtr.Zero && !_iconAdded)
        {
            if (!DestroyIcon(_iconHandle))
                errors.Add(LastError("DestroyIcon"));
            else
                _iconHandle = IntPtr.Zero;
        }

        FreeMenuAccessibilityData();
        return errors;
    }

    private void ThrowIfDisposed()
    {
        if (_disposed)
            throw new ObjectDisposedException(nameof(TrayIcon));
    }

    private static Exception LastError(string operation)
    {
        int error = Marshal.GetLastWin32Error();
        return error == 0
            ? new IOException($"{operation} failed without a Win32 error code.")
            : new Win32Exception(error, $"{operation} failed.");
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct NotifyIconData
    {
        public uint cbSize;
        public IntPtr hWnd;
        public uint uID;
        public uint uFlags;
        public uint uCallbackMessage;
        public IntPtr hIcon;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string szTip;
        public uint dwState;
        public uint dwStateMask;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)] public string szInfo;
        public uint uVersion;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)] public string szInfoTitle;
        public uint dwInfoFlags;
        public Guid guidItem;
        public IntPtr hBalloonIcon;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct Point
    {
        public int X;
        public int Y;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct Size
    {
        public int X;
        public int Y;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct Rect
    {
        public int Left;
        public int Top;
        public int Right;
        public int Bottom;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct MeasureItemStruct
    {
        public uint CtlType;
        public uint CtlID;
        public uint itemID;
        public uint itemWidth;
        public uint itemHeight;
        public UIntPtr itemData;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct DrawItemStruct
    {
        public uint CtlType;
        public uint CtlID;
        public uint itemID;
        public uint itemAction;
        public uint itemState;
        public IntPtr hwndItem;
        public IntPtr hDC;
        public Rect rcItem;
        public UIntPtr itemData;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct MenuItemInfo
    {
        public uint cbSize;
        public uint fMask;
        public uint fType;
        public uint fState;
        public uint wID;
        public IntPtr hSubMenu;
        public IntPtr hbmpChecked;
        public IntPtr hbmpUnchecked;
        public UIntPtr dwItemData;
        [MarshalAs(UnmanagedType.LPWStr)] public string dwTypeData;
        public uint cch;
        public IntPtr hbmpItem;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct MsaaMenuInfo
    {
        public uint dwMSAASignature;
        public uint cchWText;
        public IntPtr pszWText;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct CreateStruct
    {
        public IntPtr lpCreateParams;
        public IntPtr hInstance;
        public IntPtr hMenu;
        public IntPtr hwndParent;
        public int cy;
        public int cx;
        public int y;
        public int x;
        public int style;
        public IntPtr lpszName;
        public IntPtr lpszClass;
        public uint dwExStyle;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct WindowClassEx
    {
        public uint cbSize;
        public uint style;
        public MenuOwnerProc? lpfnWndProc;
        public int cbClsExtra;
        public int cbWndExtra;
        public IntPtr hInstance;
        public IntPtr hIcon;
        public IntPtr hCursor;
        public IntPtr hbrBackground;
        public string? lpszMenuName;
        public string lpszClassName;
        public IntPtr hIconSm;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct LogFont
    {
        public int lfHeight;
        public int lfWidth;
        public int lfEscapement;
        public int lfOrientation;
        public int lfWeight;
        public byte lfItalic;
        public byte lfUnderline;
        public byte lfStrikeOut;
        public byte lfCharSet;
        public byte lfOutPrecision;
        public byte lfClipPrecision;
        public byte lfQuality;
        public byte lfPitchAndFamily;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string lfFaceName;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct NonClientMetrics
    {
        public uint cbSize;
        public int iBorderWidth;
        public int iScrollWidth;
        public int iScrollHeight;
        public int iCaptionWidth;
        public int iCaptionHeight;
        public LogFont lfCaptionFont;
        public int iSmCaptionWidth;
        public int iSmCaptionHeight;
        public LogFont lfSmCaptionFont;
        public int iMenuWidth;
        public int iMenuHeight;
        public LogFont lfMenuFont;
        public LogFont lfStatusFont;
        public LogFont lfMessageFont;
        public int iPaddedBorderWidth;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct HighContrast
    {
        public uint cbSize;
        public uint dwFlags;
        public IntPtr lpszDefaultScheme;
    }

    [UnmanagedFunctionPointer(CallingConvention.Winapi)]
    private delegate IntPtr SubclassProc(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam, UIntPtr subclassId, UIntPtr refData);

    [UnmanagedFunctionPointer(CallingConvention.Winapi)]
    private delegate IntPtr MenuOwnerProc(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll", EntryPoint = "IsWindow", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool IsWindow(IntPtr hwnd);

    [DllImport("kernel32.dll", EntryPoint = "GetModuleHandleW", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr GetModuleHandleW(string? moduleName);

    [DllImport("user32.dll", EntryPoint = "RegisterClassExW", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern ushort RegisterClassExW(ref WindowClassEx windowClass);

    [DllImport("user32.dll", EntryPoint = "CreateWindowExW", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateWindowExW(uint exStyle, string className, string windowName, uint style, int x, int y, int width, int height, IntPtr parent, IntPtr menu, IntPtr instance, IntPtr parameter);

    [DllImport("user32.dll", EntryPoint = "DestroyWindow", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DestroyWindow(IntPtr hwnd);

    [DllImport("user32.dll", EntryPoint = "GetWindowLongPtrW", SetLastError = true)]
    private static extern IntPtr GetWindowLongPtrW(IntPtr hwnd, int index);

    [DllImport("user32.dll", EntryPoint = "SetWindowLongPtrW", SetLastError = true)]
    private static extern IntPtr SetWindowLongPtrW(IntPtr hwnd, int index, IntPtr newValue);

    [DllImport("user32.dll", EntryPoint = "DefWindowProcW", SetLastError = true)]
    private static extern IntPtr DefWindowProcW(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll", EntryPoint = "SetWindowPos", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetWindowPos(IntPtr hwnd, IntPtr insertAfter, int x, int y, int width, int height, uint flags);

    [DllImport("user32.dll", EntryPoint = "RegisterWindowMessageW", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern uint RegisterWindowMessageW(string message);

    [DllImport("user32.dll", EntryPoint = "LoadImageW", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr LoadImageW(IntPtr instance, string name, uint type, int width, int height, uint flags);

    [DllImport("user32.dll", EntryPoint = "DestroyIcon", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DestroyIcon(IntPtr icon);

    [DllImport("shell32.dll", EntryPoint = "Shell_NotifyIconW", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool Shell_NotifyIconW(uint message, ref NotifyIconData data);

    [DllImport("comctl32.dll", EntryPoint = "SetWindowSubclass", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetWindowSubclass(IntPtr hwnd, SubclassProc callback, UIntPtr subclassId, UIntPtr refData);

    [DllImport("comctl32.dll", EntryPoint = "RemoveWindowSubclass", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool RemoveWindowSubclass(IntPtr hwnd, SubclassProc callback, UIntPtr subclassId);

    [DllImport("comctl32.dll", EntryPoint = "DefSubclassProc", SetLastError = true)]
    private static extern IntPtr DefSubclassProc(IntPtr hwnd, uint message, UIntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll", EntryPoint = "CreatePopupMenu", SetLastError = true)]
    private static extern IntPtr CreatePopupMenu();

    [DllImport("user32.dll", EntryPoint = "InsertMenuItemW", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool InsertMenuItemW(IntPtr menu, uint item, [MarshalAs(UnmanagedType.Bool)] bool byPosition, ref MenuItemInfo itemInfo);

    [DllImport("user32.dll", EntryPoint = "TrackPopupMenuEx", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool TrackPopupMenuEx(IntPtr menu, uint flags, int x, int y, IntPtr hwnd, IntPtr parameters);

    [DllImport("user32.dll", EntryPoint = "DestroyMenu", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DestroyMenu(IntPtr menu);

    [DllImport("user32.dll", EntryPoint = "EndMenu", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool EndMenu();

    [DllImport("user32.dll", EntryPoint = "GetCursorPos", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetCursorPos(out Point point);

    [DllImport("user32.dll", EntryPoint = "SetForegroundWindow", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetForegroundWindow(IntPtr hwnd);

    [DllImport("user32.dll", EntryPoint = "GetDpiForWindow", SetLastError = true)]
    private static extern uint GetDpiForWindow(IntPtr hwnd);

    [DllImport("user32.dll", EntryPoint = "SystemParametersInfoForDpi", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SystemParametersInfoForDpi(uint action, uint parameter, ref NonClientMetrics data, uint update, uint dpi);

    [DllImport("user32.dll", EntryPoint = "SystemParametersInfoW", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SystemParametersInfoW(uint action, uint parameter, ref HighContrast data, uint update);

    [DllImport("gdi32.dll", EntryPoint = "CreateFontIndirectW", SetLastError = true)]
    private static extern IntPtr CreateFontIndirectW(ref LogFont font);

    [DllImport("gdi32.dll", EntryPoint = "CreateSolidBrush", SetLastError = true)]
    private static extern IntPtr CreateSolidBrush(uint color);

    [DllImport("gdi32.dll", EntryPoint = "DeleteObject", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool DeleteObject(IntPtr item);

    [DllImport("gdi32.dll", EntryPoint = "SelectObject", SetLastError = true)]
    private static extern IntPtr SelectObject(IntPtr dc, IntPtr item);

    [DllImport("gdi32.dll", EntryPoint = "GetTextExtentPoint32W", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetTextExtentPoint32W(IntPtr dc, string text, int count, out Size size);

    [DllImport("gdi32.dll", EntryPoint = "SetTextColor", SetLastError = true)]
    private static extern uint SetTextColor(IntPtr dc, uint color);

    [DllImport("gdi32.dll", EntryPoint = "SetBkMode", SetLastError = true)]
    private static extern int SetBkMode(IntPtr dc, int mode);

    [DllImport("gdi32.dll", EntryPoint = "SaveDC", SetLastError = true)]
    private static extern int SaveDC(IntPtr dc);

    [DllImport("gdi32.dll", EntryPoint = "RestoreDC", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool RestoreDC(IntPtr dc, int savedDc);

    [DllImport("user32.dll", EntryPoint = "GetDC", SetLastError = true)]
    private static extern IntPtr GetDC(IntPtr hwnd);

    [DllImport("user32.dll", EntryPoint = "ReleaseDC", SetLastError = true)]
    private static extern int ReleaseDC(IntPtr hwnd, IntPtr dc);

    [DllImport("user32.dll", EntryPoint = "FillRect", SetLastError = true)]
    private static extern int FillRect(IntPtr dc, ref Rect rectangle, IntPtr brush);

    [DllImport("user32.dll", EntryPoint = "GetSysColor", SetLastError = true)]
    private static extern uint GetSysColor(int index);

    [DllImport("user32.dll", EntryPoint = "DrawTextW", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern int DrawTextW(IntPtr dc, string text, int count, ref Rect rectangle, uint format);
}
