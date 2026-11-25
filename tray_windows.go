package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	trayWindowBaseClass = "UUDisplaySwitch.ResidentTray.Window.v1"
	trayControlMessage  = "UUDisplaySwitch.Resident.TrayControl.v1"
)

const (
	trayControlStop     uint32 = 1
	trayControlActivate uint32 = 2
	trayControlOpenUI   uint32 = 3
)

const (
	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004
	nifMessage    = 0x00000001
	nifIcon       = 0x00000002
	nifTip        = 0x00000004
	nifInfo       = 0x00000010
	nifShowTip    = 0x00000080
	niifInfo      = 0x00000001
	niifError     = 0x00000003
	notifyVersion4 = 4

	imageIcon = 1

	wsPopup        = 0x80000000
	wsExToolWindow = 0x00000080
	swHide         = 0

	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmQuit          = 0x0012
	wmNull          = 0x0000
	wmDrawItem      = 0x002B
	wmMeasureItem   = 0x002C
	wmDeviceChange  = 0x0219
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203
	wmContextMenu   = 0x007B
	ninSelect       = 0x0400
	ninKeySelect    = 0x0401

	dbtDevnodesChanged = 0x0007
	msgfltAllow        = 1
	qsAllInput         = 0x04FF
	pmRemove           = 0x0001

	mfOwnerDraw      = 0x00000100
	mfDisabled       = 0x00000002
	mfGrayed         = 0x00000001
	mfSeparator      = 0x00000800
	tpmRightButton   = 0x0002
	tpmReturnCommand = 0x0100

	odtMenu     = 1
	odsSelected = 0x0001
	odsDisabled = 0x0004
	odsGrayed   = 0x0002

	dtSingleLine = 0x00000020
	dtVCenter    = 0x00000004
	dtLeft       = 0x00000000
	dtNoPrefix   = 0x00000800

	mbYesNo       = 0x00000004
	mbIconError   = 0x00000010
	mbIconWarning = 0x00000030
	mbDefButton2  = 0x00000100
	mbSetForegnd  = 0x00010000
	mbTopMost     = 0x00040000
	idYes         = 6

	smtoBlock       = 0x0001
	smtoAbortIfHung = 0x0002

	processQueryLimitedInformation = 0x1000
	waitObject0                    = 0x00000000
	waitTimeout                    = 0x00000102
	waitAbandoned                  = 0x00000080

	spiGetHighContrast = 0x0042
	hcfHighContrastOn  = 0x00000001
	colorMenu          = 4
	colorMenuText      = 7
	colorGrayText      = 17
	colorHighlight     = 13
	colorHighlightText = 14

	trayIconID         = 1
	trayMenuStatusID   = 0x6300
	trayMenuSepID      = 0x630F
	trayMenuSettingsID = 0x6301
	trayMenuExitID     = 0x6302
)

var (
	user32Tray   = windows.NewLazySystemDLL("user32.dll")
	shell32Tray  = windows.NewLazySystemDLL("shell32.dll")
	kernel32Tray = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW         = user32Tray.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32Tray.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32Tray.NewProc("DefWindowProcW")
	procDestroyWindow            = user32Tray.NewProc("DestroyWindow")
	procShowWindow               = user32Tray.NewProc("ShowWindow")
	procPeekMessageW               = user32Tray.NewProc("PeekMessageW")
	procMsgWaitForMultipleObjects  = user32Tray.NewProc("MsgWaitForMultipleObjects")
	procTranslateMessage           = user32Tray.NewProc("TranslateMessage")
	procDispatchMessageW         = user32Tray.NewProc("DispatchMessageW")
	procPostQuitMessage          = user32Tray.NewProc("PostQuitMessage")
	procRegisterWindowMessageW   = user32Tray.NewProc("RegisterWindowMessageW")
	procFindWindowW              = user32Tray.NewProc("FindWindowW")
	procGetWindowThreadProcessID = user32Tray.NewProc("GetWindowThreadProcessId")
	procPostMessageW             = user32Tray.NewProc("PostMessageW")
	procSendMessageTimeoutW      = user32Tray.NewProc("SendMessageTimeoutW")
	procCreatePopupMenu            = user32Tray.NewProc("CreatePopupMenu")
	procAppendMenuW                = user32Tray.NewProc("AppendMenuW")
	procTrackPopupMenu             = user32Tray.NewProc("TrackPopupMenu")
	procDestroyMenu                = user32Tray.NewProc("DestroyMenu")
	procEndMenu                    = user32Tray.NewProc("EndMenu")
	procSetForegroundWindow        = user32Tray.NewProc("SetForegroundWindow")
	procGetCursorPos               = user32Tray.NewProc("GetCursorPos")
	procMessageBoxW                = user32Tray.NewProc("MessageBoxW")
	procLoadImageW                 = user32Tray.NewProc("LoadImageW")
	procDestroyIcon                = user32Tray.NewProc("DestroyIcon")
	procDrawTextW                  = user32Tray.NewProc("DrawTextW")
	procGetSysColor                = user32Tray.NewProc("GetSysColor")
	procSystemParametersInfoW      = user32Tray.NewProc("SystemParametersInfoW")
	procChangeWindowMessageFilterEx = user32Tray.NewProc("ChangeWindowMessageFilterEx")
	procGetTextExtentPoint32W      = gdi32.NewProc("GetTextExtentPoint32W")
	procGetModuleHandleW           = kernel32Tray.NewProc("GetModuleHandleW")
	procCreateEventW               = kernel32Tray.NewProc("CreateEventW")
	procSetEvent                   = kernel32Tray.NewProc("SetEvent")
	procResetEvent                 = kernel32Tray.NewProc("ResetEvent")
	procRtlMoveMemory              = kernel32Tray.NewProc("RtlMoveMemory")
	procShellNotifyIconW           = shell32Tray.NewProc("Shell_NotifyIconW")
	trayWndProc                    = windows.NewCallback(residentWindowProc)
	currentResidentTray            *residentTray
)

func copyFromWin32(dst unsafe.Pointer, src uintptr, size uintptr) {
	if dst != nil && src != 0 && size != 0 {
		procRtlMoveMemory.Call(uintptr(dst), src, size)
		runtime.KeepAlive(dst)
	}
}

func copyToWin32(dst uintptr, src unsafe.Pointer, size uintptr) {
	if dst != 0 && src != nil && size != 0 {
		procRtlMoveMemory.Call(dst, uintptr(src), size)
		runtime.KeepAlive(src)
	}
}

type notifyIconData struct {
	cbSize            uint32
	hWnd              uintptr
	uID               uint32
	uFlags            uint32
	uCallbackMessage  uint32
	hIcon             uintptr
	szTip             [128]uint16
	dwState           uint32
	dwStateMask       uint32
	szInfo            [256]uint16
	uTimeoutOrVersion uint32
	szInfoTitle       [64]uint16
	dwInfoFlags       uint32
	guidItem          [16]byte
	hBalloonIcon      uintptr
}

type trayWindowClass struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type trayPoint struct {
	x int32
	y int32
}

type traySize struct {
	cx int32
	cy int32
}

type trayMessage struct {
	hWnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      trayPoint
	private uint32
}

type measureItemStruct struct {
	ctlType    uint32
	ctlID      uint32
	itemID     uint32
	itemWidth  uint32
	itemHeight uint32
	itemData   uintptr
}

type drawItemStruct struct {
	ctlType    uint32
	ctlID      uint32
	itemID     uint32
	itemAction uint32
	itemState  uint32
	hwndItem   uintptr
	hDC        uintptr
	rcItem     winRect
	itemData   uintptr
}

type highContrastInfo struct {
	cbSize            uint32
	dwFlags           uint32
	lpszDefaultScheme *uint16
}

type residentTray struct {
	hwnd            uintptr
	icons           trayIconSet
	mutex           windows.Handle
	readyEvent      windows.Handle
	uiWakeEvt       windows.Handle
	monitorEvtStop  windows.Handle
	monitorEvtDone  chan struct{}
	devSubscription *deviceNotificationSubscription
	iconData        notifyIconData
	callbackMessage uint32
	taskbarCreated  uint32
	controlMessage  uint32

	settings     userSettings
	cachedState  appState
	enabledAfter int64
	startupKnown bool
	startupOn    bool

	settingsWin *settingsWindow

	menuFont       uintptr
	menuStatusLine string
	menuDark       bool
	menuOpen       bool

	toggling             atomic.Bool
	devChangePending     atomic.Bool
	monWakePending       atomic.Bool
	checkingAutoExit     bool
	monitorEventPending  bool
	refreshDeviceBusy    bool
	refreshDevicePending bool
	reportingError       bool
	iconAdded            bool
	readySignaled        bool
	shuttingDown         bool
	shutdownOnce         sync.Once
	shutdownCh           chan struct{}
	stateForMonitorEvent func() (appState, userSettings, error)

	uiQueueMu sync.Mutex
	uiQueue   []func()

	lastIconErr string
}

type trayStatus struct {
	Ready   bool   `json:"ready"`
	PID     uint32 `json:"pid"`
	Message string `json:"message"`
}

func timeMillisNow() int64 {
	return time.Now().UnixMilli()
}

func trayMutexFullName() string {
	return kernelObjectName("ResidentTray")
}

func trayReadyEventFullName() string {
	return kernelObjectName("ResidentTray.Ready")
}

func trayWindowClassFullName() string {
	return windowClassForNamespace(trayWindowBaseClass)
}

// trayCallbackAction routes notification-area mouse/keyboard callbacks:
// - Left single click (NIN_SELECT) or keyboard Enter/Space (NIN_KEYSELECT) -> direct toggle
// - Right click / context menu (WM_RBUTTONUP / WM_CONTEXTMENU) -> settings / exit menu
// - Double-click (WM_LBUTTONDBLCLK) is intentionally ignored so fast clicks never double-toggle.
func trayCallbackAction(callback uint32, expectedID uint16) (string, bool) {
	event := uint16(callback)
	iconID := uint16(callback >> 16)
	if iconID != expectedID {
		return "", false
	}
	switch event {
	case wmContextMenu, wmRButtonUp:
		return "menu", true
	case ninSelect, ninKeySelect:
		return "toggle", true
	default:
		return "", false
	}
}

func shouldConfirmRemoteDisable(mode string, uu uuSnapshot, scanErr error) bool {
	return mode == "disable" && scanErr == nil && uu.Running
}

func nextToggleMode(problem uint32) (string, error) {
	switch problem {
	case 0:
		return "disable", nil
	case 22:
		return "enable", nil
	default:
		return "", fmt.Errorf("设备存在其他故障（代码 %d），未执行切换。", problem)
	}
}

func trayShouldAutoExit(preferences autoPreferences, state appState, enabledAfter int64) bool {
	return preferences.AutoExit &&
		state.UUStateKnown && !state.UURunning && !state.UUUpdating &&
		!state.WatcherActive && state.WatcherStatus != "error" &&
		state.UUExitAt > enabledAfter &&
		(!preferences.AutoDisable || state.Status == "disabled")
}

func trayCorePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Clean(exe), nil
}

func normalizedTrayPath(path string) string {
	path = filepath.Clean(strings.TrimPrefix(path, "\\\\?\\"))
	return strings.ToLower(path)
}

func sameTrayPath(first, second string) bool {
	return normalizedTrayPath(first) == normalizedTrayPath(second)
}

func processImagePath(pid uint32) (string, error) {
	process, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return "", fmt.Errorf("Could not open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	path, err := queryHandleImagePath(process)
	if err != nil {
		return "", fmt.Errorf("Could not verify process %d image: %w", pid, err)
	}
	return path, nil
}

func windowProcessID(hwnd uintptr) (uint32, error) {
	var pid uint32
	thread, _, callErr := procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if thread == 0 || pid == 0 {
		return 0, win32CallError("GetWindowThreadProcessId", callErr)
	}
	return pid, nil
}

func findResidentWindow() (uintptr, error) {
	className, err := windows.UTF16PtrFromString(trayWindowClassFullName())
	if err != nil {
		return 0, err
	}
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
	runtime.KeepAlive(className)
	return hwnd, nil
}

func residentImage(hwnd uintptr) (uint32, string, error) {
	pid, err := windowProcessID(hwnd)
	if err != nil {
		return 0, "", err
	}
	image, err := processImagePath(pid)
	if err != nil {
		return pid, "", err
	}
	return pid, image, nil
}

func eventReady(event windows.Handle) (bool, error) {
	result, err := windows.WaitForSingleObject(event, 0)
	if err != nil {
		return false, err
	}
	switch result {
	case waitObject0:
		return true, nil
	case waitTimeout:
		return false, nil
	default:
		return false, fmt.Errorf("Unexpected tray readiness event result %d", result)
	}
}

func trayStatusNow() trayStatus {
	status := trayStatus{}
	eventName, err := windows.UTF16PtrFromString(trayReadyEventFullName())
	if err != nil {
		status.Message = err.Error()
		return status
	}
	event, err := windows.OpenEvent(windows.SYNCHRONIZE, false, eventName)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return status
		}
		status.Message = err.Error()
		return status
	}
	defer windows.CloseHandle(event)
	signaled, err := eventReady(event)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	if !signaled {
		return status
	}

	mutexName, err := windows.UTF16PtrFromString(trayMutexFullName())
	if err != nil {
		status.Message = err.Error()
		return status
	}
	mutex, err := windows.OpenMutex(windows.SYNCHRONIZE, false, mutexName)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return status
		}
		status.Message = err.Error()
		return status
	}
	defer windows.CloseHandle(mutex)

	hwnd, err := findResidentWindow()
	if err != nil {
		status.Message = err.Error()
		return status
	}
	if hwnd == 0 {
		status.Message = "The tray readiness event exists without its resident window."
		return status
	}
	pid, image, err := residentImage(hwnd)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	core, err := trayCorePath()
	if err != nil {
		status.Message = err.Error()
		return status
	}
	if !sameTrayPath(core, image) {
		status.Message = fmt.Sprintf("A tray resident from another application path is active: %s", image)
		return status
	}
	status.Ready = true
	status.PID = pid
	return status
}

func win32CallError(operation string, callErr error) error {
	if callErr == nil || errors.Is(callErr, syscall.Errno(0)) {
		callErr = windows.GetLastError()
	}
	if callErr == nil || errors.Is(callErr, syscall.Errno(0)) {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s failed: %w", operation, callErr)
}

func registerTrayMessage(name string) (uint32, error) {
	messageName, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	message, _, callErr := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(messageName)))
	runtime.KeepAlive(messageName)
	if message == 0 {
		return 0, win32CallError("RegisterWindowMessageW", callErr)
	}
	return uint32(message), nil
}

func createReadyEvent() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(trayReadyEventFullName())
	if err != nil {
		return 0, err
	}
	event, _, callErr := procCreateEventW.Call(0, 1, 0, uintptr(unsafe.Pointer(name)))
	runtime.KeepAlive(name)
	if event == 0 {
		return 0, win32CallError("CreateEventW(tray readiness)", callErr)
	}
	return windows.Handle(event), nil
}

func createResidentMutex() (windows.Handle, bool, error) {
	name, err := windows.UTF16PtrFromString(trayMutexFullName())
	if err != nil {
		return 0, false, err
	}
	mutex, err := windows.CreateMutex(nil, true, name)
	if err == nil {
		return mutex, true, nil
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if mutex != 0 {
			_ = windows.CloseHandle(mutex)
		}
		return 0, false, err
	}
	return mutex, false, nil
}

var errResidentMutexAcquired = errors.New("resident mutex acquired after the previous owner exited")

func notifyExistingResident(mutex windows.Handle, openSettings bool) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		hwnd, err := findResidentWindow()
		if err != nil {
			return err
		}
		if hwnd != 0 {
			_, image, imageErr := residentImage(hwnd)
			if imageErr == nil {
				core, coreErr := trayCorePath()
				if coreErr != nil {
					return coreErr
				}
				if !sameTrayPath(core, image) {
					return fmt.Errorf("A tray resident from another UU Display installation is active: %s", image)
				}
				if status := trayStatusNow(); status.Ready {
					if msg, msgErr := registerTrayMessage(trayControlMessage); msgErr == nil {
						cmd := trayControlActivate
						if openSettings {
							cmd = trayControlOpenUI
						}
						procPostMessageW.Call(hwnd, uintptr(msg), uintptr(cmd), 0)
					}
					return nil
				}
			}
		}
		result, waitErr := windows.WaitForSingleObject(mutex, 0)
		if waitErr != nil {
			return fmt.Errorf("Could not verify the existing tray resident: %w", waitErr)
		}
		if result == waitObject0 || result == waitAbandoned {
			return errResidentMutexAcquired
		}
		if result != waitTimeout {
			return fmt.Errorf("Unexpected resident mutex result %d", result)
		}
		if !time.Now().Before(deadline) {
			return errors.New("The resident tray did not become ready before the startup timeout.")
		}
		time.Sleep(80 * time.Millisecond)
	}
}

func runResidentTray(openSettingsOnStart bool, onReady func(*residentTray)) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	prevMaxProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(prevMaxProcs)

	mutex, ownsMutex, err := createResidentMutex()
	if err != nil {
		return fmt.Errorf("Could not create resident tray mutex: %w", err)
	}
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if !ownsMutex {
		err := notifyExistingResident(mutex, openSettingsOnStart)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errResidentMutexAcquired) {
			return err
		}
		ownsMutex = true
	}
	defer windows.ReleaseMutex(mutex)

	event, err := createReadyEvent()
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	if ready, readyErr := eventReady(event); readyErr != nil {
		return readyErr
	} else if ready {
		if result, _, callErr := procResetEvent.Call(uintptr(event)); result == 0 {
			return win32CallError("ResetEvent(stale tray readiness)", callErr)
		}
	}

	wakeRaw, _, wakeErr := procCreateEventW.Call(0, 0, 0, 0)
	if wakeRaw == 0 {
		return win32CallError("CreateEventW(tray ui wake)", wakeErr)
	}

	tray := &residentTray{
		mutex:        mutex,
		readyEvent:   event,
		uiWakeEvt:    windows.Handle(wakeRaw),
		settings:     defaultUserSettings(),
		cachedState:  appState{Device: deviceName, WatcherStatus: "stopped"},
		enabledAfter: timeMillisNow(),
		shutdownCh:   make(chan struct{}),
	}

	tray.controlMessage, err = registerTrayMessage(trayControlMessage)
	if err != nil {
		_ = windows.CloseHandle(tray.uiWakeEvt)
		tray.uiWakeEvt = 0
		return err
	}
	tray.callbackMessage, err = registerTrayMessage("UUDisplaySwitch.Resident.Callback.v1")
	if err != nil {
		_ = windows.CloseHandle(tray.uiWakeEvt)
		tray.uiWakeEvt = 0
		return err
	}
	tray.taskbarCreated, err = registerTrayMessage("TaskbarCreated")
	if err != nil {
		_ = windows.CloseHandle(tray.uiWakeEvt)
		tray.uiWakeEvt = 0
		return err
	}
	if err := tray.createWindow(); err != nil {
		_ = windows.CloseHandle(tray.uiWakeEvt)
		tray.uiWakeEvt = 0
		return err
	}
	currentResidentTray = tray
	defer tray.cleanup()

	if err := tray.addIcon(); err != nil {
		return err
	}

	if result, _, callErr := procSetEvent.Call(uintptr(event)); result == 0 {
		return win32CallError("SetEvent(tray readiness)", callErr)
	}
	tray.readySignaled = true

	// Post-ready initialization: keeps cold-start readiness minimal while enforcing runtime safety before monitor/disk writes.
	if err := requireDesktopRuntime(!globalReadOnly); err != nil {
		return err
	}
	var initialSettingsErr error
	tray.settings, err = loadUserSettings()
	if err != nil {
		initialSettingsErr = fmt.Errorf("Could not read saved settings: %w", err)
		tray.settings = defaultUserSettings()
	}
	applyProcessPreferredAppMode(tray.settings.Theme)

	// Event-driven kernel subscriptions: zero idle CPU polling and zero worker PostMessageW.
	tray.devSubscription = subscribeDeviceNotifications(tray.uiWakeEvt, &tray.devChangePending)
	tray.startMonitorEventWaiter()

	if initialSettingsErr != nil {
		tray.showBalloon(getStrings(tray.settings.Language).errorTitle, initialSettingsErr.Error(), niifError)
	} else {
		hwnd := tray.hwnd
		wantMonitor := !globalReadOnly && tray.settings.monitorEnabled()
		go func() {
			dev := readDeviceOnlyState()
			tray.postUIUpdate(func() {
				if !tray.isToggling() {
					tray.applyDeviceState(dev)
				}
				tray.icons.ensureBadges()
				_ = tray.refreshIconAndTooltip()
				if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
					tray.settingsWin.updateLiveStateDisplay()
				}
			})
			if wantMonitor {
				if startErr := ensureMonitorRunning(hwnd, tray.requestAutoExitShutdown); startErr != nil {
					tray.postUIUpdate(func() {
						tray.reportError("monitor-autostart", startErr)
					})
					return
				}
			}
			st := readState(false)
			tray.postUIUpdate(func() {
				tray.applyFreshState(st)
			})
		}()
	}

	if openSettingsOnStart {
		tray.openOrFocusSettings()
	}
	if onReady != nil {
		onReady(tray)
	}
	return tray.messageLoop()
}

func (tray *residentTray) wakeUI() {
	if tray == nil {
		return
	}
	tray.uiQueueMu.Lock()
	if !tray.shuttingDown && tray.uiWakeEvt != 0 {
		_ = windows.SetEvent(tray.uiWakeEvt)
	}
	tray.uiQueueMu.Unlock()
}

func (tray *residentTray) signalMonitorStateEvent() {
	tray.monWakePending.Store(true)
	tray.wakeUI()
}

func (tray *residentTray) startMonitorEventWaiter() {
	monEvt, err := createOrOpenSharedEvent("MonitorStateChanged", false)
	if err != nil || monEvt == 0 {
		return
	}
	stopEvt, _, _ := procCreateEventW.Call(0, 1, 0, 0)
	if stopEvt == 0 {
		_ = windows.CloseHandle(monEvt)
		return
	}
	doneCh := make(chan struct{})
	tray.monitorEvtStop = windows.Handle(stopEvt)
	tray.monitorEvtDone = doneCh
	setInProcessMonitorStateWaiterActive(true)
	go func() {
		defer close(doneCh)
		defer setInProcessMonitorStateWaiterActive(false)
		defer windows.CloseHandle(monEvt)
		handles := []windows.Handle{windows.Handle(stopEvt), monEvt}
		for {
			res, waitErr := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
			if waitErr != nil || res == windows.WAIT_OBJECT_0 {
				return
			}
			if res == windows.WAIT_OBJECT_0+1 {
				notifyInProcessMonitorStateWaiters()
				tray.signalMonitorStateEvent()
			}
		}
	}()
}

func (tray *residentTray) postUIUpdate(fn func()) {
	if tray == nil || fn == nil {
		return
	}
	tray.uiQueueMu.Lock()
	if !tray.shuttingDown {
		tray.uiQueue = append(tray.uiQueue, fn)
		if tray.uiWakeEvt != 0 {
			_ = windows.SetEvent(tray.uiWakeEvt)
		}
	}
	tray.uiQueueMu.Unlock()
}

func (tray *residentTray) drainPendingUI() {
	if tray.devChangePending.Swap(false) {
		tray.refreshDeviceOnlyAsync()
	}
	if tray.monWakePending.Swap(false) {
		tray.handleMonitorStateEvent()
	}
	tray.drainUIUpdates()
}

func (tray *residentTray) drainUIUpdates() {
	for {
		tray.uiQueueMu.Lock()
		if len(tray.uiQueue) == 0 {
			tray.uiQueueMu.Unlock()
			return
		}
		batch := tray.uiQueue
		tray.uiQueue = nil
		tray.uiQueueMu.Unlock()
		for _, fn := range batch {
			if !tray.shuttingDown {
				fn()
			}
		}
	}
}

func (tray *residentTray) requestAutoExitShutdown() {
	tray.postUIUpdate(func() {
		_ = tray.shutdown()
	})
}

func (tray *residentTray) isToggling() bool {
	return tray.toggling.Load()
}

func (tray *residentTray) currentTipText() string {
	s := getStrings(tray.settings.Language)
	if tray.isToggling() {
		return s.tipSwitching
	}
	switch tray.cachedState.Status {
	case "":
		return s.app
	case "enabled":
		return s.tipEnabled
	case "disabled":
		return s.tipDisabled
	default:
		return s.tipUnavailable
	}
}

func (tray *residentTray) createWindow() error {
	className, err := windows.UTF16PtrFromString(trayWindowClassFullName())
	if err != nil {
		return err
	}
	module, _, moduleErr := procGetModuleHandleW.Call(0)
	if module == 0 {
		return win32CallError("GetModuleHandleW", moduleErr)
	}
	windowClass := trayWindowClass{
		cbSize:        uint32(unsafe.Sizeof(trayWindowClass{})),
		lpfnWndProc:   trayWndProc,
		hInstance:     module,
		lpszClassName: className,
	}
	registered, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&windowClass)))
	runtime.KeepAlive(className)
	runtime.KeepAlive(&windowClass)
	if registered == 0 && !errors.Is(callErr, syscall.Errno(1410)) {
		return win32CallError("RegisterClassExW(resident tray)", callErr)
	}
	hwnd, _, callErr := procCreateWindowExW.Call(
		wsExToolWindow,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		wsPopup,
		0, 0, 0, 0,
		0, 0, module, 0,
	)
	runtime.KeepAlive(className)
	if hwnd == 0 {
		return win32CallError("CreateWindowExW(resident tray)", callErr)
	}
	tray.hwnd = hwnd
	if procChangeWindowMessageFilterEx.Find() == nil {
		if tray.controlMessage != 0 {
			procChangeWindowMessageFilterEx.Call(hwnd, uintptr(tray.controlMessage), msgfltAllow, 0)
		}
		if tray.taskbarCreated != 0 {
			procChangeWindowMessageFilterEx.Call(hwnd, uintptr(tray.taskbarCreated), msgfltAllow, 0)
		}
	}
	procShowWindow.Call(hwnd, swHide)
	return nil
}

func (tray *residentTray) addIcon() error {
	dpi := windowDPI(tray.hwnd)
	if tray.icons.baseSmall == 0 || tray.icons.dpi != dpi {
		tray.icons.Destroy()
		if tray.cachedState.Status == "" {
			tray.icons = newBaseTrayIconSet(dpi)
		} else {
			tray.icons = newTrayIconSet(dpi)
		}
	}
	iconHandle := tray.icons.iconForState(tray.cachedState.Status, tray.isToggling())
	if iconHandle == 0 {
		return errors.New("tray icon resource is unavailable")
	}
	tray.iconData = notifyIconData{
		cbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		hWnd:             tray.hwnd,
		uID:              trayIconID,
		uFlags:           nifMessage | nifIcon | nifTip | nifShowTip,
		uCallbackMessage: tray.callbackMessage,
		hIcon:            iconHandle,
	}
	copy(tray.iconData.szTip[:], toUTF16Array(tray.currentTipText(), len(tray.iconData.szTip)))
	if err := tray.shellNotify(nimAdd); err != nil {
		return err
	}
	tray.iconAdded = true
	tray.iconData.uTimeoutOrVersion = notifyVersion4
	if err := tray.shellNotify(nimSetVersion); err != nil {
		if deleteErr := tray.shellNotify(nimDelete); deleteErr == nil {
			tray.iconAdded = false
			return err
		} else {
			return fmt.Errorf("%w; cleanup failed: %v", err, deleteErr)
		}
	}
	return nil
}

func (tray *residentTray) refreshIconAndTooltip() error {
	if !tray.iconAdded {
		return nil
	}
	dpi := windowDPI(tray.hwnd)
	if tray.icons.dpi != dpi || tray.icons.baseSmall == 0 {
		tray.icons.Destroy()
		tray.icons = newTrayIconSet(dpi)
	} else if tray.cachedState.Status != "" {
		tray.icons.ensureBadges()
	}
	tray.iconData.uFlags = nifIcon | nifTip | nifShowTip
	tray.iconData.hIcon = tray.icons.iconForState(tray.cachedState.Status, tray.isToggling())
	copy(tray.iconData.szTip[:], toUTF16Array(tray.currentTipText(), len(tray.iconData.szTip)))
	return tray.shellNotify(nimModify)
}

func (tray *residentTray) showBalloon(title, body string, infoFlags uint32) {
	if !tray.iconAdded || globalReadOnly {
		return
	}
	tray.iconData.uFlags = nifInfo | nifTip | nifShowTip
	tray.iconData.dwInfoFlags = infoFlags
	copy(tray.iconData.szTip[:], toUTF16Array(tray.currentTipText(), len(tray.iconData.szTip)))
	copy(tray.iconData.szInfoTitle[:], toUTF16Array(title, len(tray.iconData.szInfoTitle)))
	copy(tray.iconData.szInfo[:], toUTF16Array(body, len(tray.iconData.szInfo)))
	_ = tray.shellNotify(nimModify)
}

func (tray *residentTray) applyDeviceState(dev appState) {
	tray.cachedState.Device = dev.Device
	tray.cachedState.CheckedAt = dev.CheckedAt
	tray.cachedState.Problem = dev.Problem
	tray.cachedState.Status = dev.Status
	tray.cachedState.Message = dev.Message
}

func (tray *residentTray) applyFreshState(st appState) {
	if tray.isToggling() {
		st.Status = tray.cachedState.Status
		st.Problem = tray.cachedState.Problem
		st.Message = tray.cachedState.Message
	}
	tray.cachedState = st
	_ = tray.refreshIconAndTooltip()
	if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
		tray.settingsWin.updateLiveStateDisplay()
	}
}

func (tray *residentTray) refreshDeviceOnlyAsync() {
	if tray.shuttingDown || tray.isToggling() {
		return
	}
	if tray.refreshDeviceBusy {
		tray.refreshDevicePending = true
		return
	}
	tray.refreshDeviceBusy = true
	tray.refreshDevicePending = false
	go func() {
		dev := readDeviceOnlyState()
		tray.postUIUpdate(func() {
			tray.refreshDeviceBusy = false
			if !tray.isToggling() {
				tray.applyDeviceState(dev)
				_ = tray.refreshIconAndTooltip()
				if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
					tray.settingsWin.updateLiveStateDisplay()
				}
			}
			if tray.refreshDevicePending && !tray.shuttingDown && !tray.isToggling() {
				tray.refreshDevicePending = false
				tray.refreshDeviceOnlyAsync()
			} else {
				tray.refreshDevicePending = false
			}
		})
	}()
}

func (tray *residentTray) handleMonitorStateEvent() {
	if tray.shuttingDown {
		return
	}
	if tray.checkingAutoExit {
		tray.monitorEventPending = true
		return
	}
	tray.checkingAutoExit = true
	tray.monitorEventPending = false
	go func() {
		var st appState
		var cfg userSettings
		var cfgErr error
		if tray.stateForMonitorEvent != nil {
			st, cfg, cfgErr = tray.stateForMonitorEvent()
		} else {
			st = readState(false)
			cfg, cfgErr = loadUserSettings()
		}
		tray.postUIUpdate(func() {
			tray.checkingAutoExit = false
			if cfgErr == nil && (tray.settingsWin == nil || !tray.settingsWin.autoBusy) {
				if !tray.settings.AutoExit && cfg.AutoExit {
					tray.enabledAfter = timeMillisNow()
				}
				tray.settings.AutoDisable = cfg.AutoDisable
				tray.settings.AutoExit = cfg.AutoExit
			}
			tray.applyFreshState(st)
			prefs := autoPreferences{AutoDisable: tray.settings.AutoDisable, AutoExit: tray.settings.AutoExit}
			if !globalReadOnly && trayShouldAutoExit(prefs, st, tray.enabledAfter) {
				_ = appendActivity(activity{Time: time.Now().Format(time.RFC3339), Mode: "auto-exit", OK: true, Message: "UU exited; closing resident tray"})
				_ = tray.shutdown()
				return
			}
			if tray.monitorEventPending && !tray.shuttingDown {
				tray.monitorEventPending = false
				tray.handleMonitorStateEvent()
			}
		})
	}()
}

func (tray *residentTray) toggleDeviceAsync(ownerHwnd uintptr) {
	if tray.shuttingDown {
		return
	}
	s := getStrings(tray.settings.Language)
	if globalReadOnly {
		tray.reportError("read-only", errors.New(s.readOnlyBlocked))
		return
	}
	if !tray.toggling.CompareAndSwap(false, true) {
		return
	}
	if ownerHwnd == 0 {
		ownerHwnd = tray.hwnd
	}

	_ = tray.refreshIconAndTooltip()
	if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
		tray.settingsWin.updateLiveStateDisplay()
	}

	go func() {
		snap, err := (nativeDevice{}).Read()
		var mode string
		if err == nil {
			mode, err = nextToggleMode(snap.Problem)
		}
		if err != nil {
			fresh := readState(false)
			tray.postUIUpdate(func() {
				tray.toggling.Store(false)
				tray.applyFreshState(fresh)
				tray.reportError("toggle-read", err)
			})
			return
		}

		// Remote blackout guard: ONLY prompt when disabling while GameViewer.exe is actively running.
		// Execute MessageBoxW synchronously on the locked UI thread to satisfy Win32 window ownership.
		if mode == "disable" {
			uu, scanErr := scanUU()
			if shouldConfirmRemoteDisable(mode, uu, scanErr) {
				confirmCh := make(chan bool, 1)
				tray.postUIUpdate(func() {
					if tray.shuttingDown {
						confirmCh <- false
						return
					}
					targetOwner := ownerHwnd
					if targetOwner != tray.hwnd {
						if ok, _, _ := procIsWindow.Call(targetOwner); ok == 0 {
							targetOwner = tray.hwnd
						}
					}
					curStrings := getStrings(tray.settings.Language)
					titlePtr, _ := windows.UTF16PtrFromString(curStrings.confirmDisableTitle)
					bodyPtr, _ := windows.UTF16PtrFromString(curStrings.confirmDisableBody)
					ans, _, _ := procMessageBoxW.Call(
						targetOwner,
						uintptr(unsafe.Pointer(bodyPtr)),
						uintptr(unsafe.Pointer(titlePtr)),
						mbYesNo|mbIconWarning|mbDefButton2|mbSetForegnd|mbTopMost,
					)
					runtime.KeepAlive(titlePtr)
					runtime.KeepAlive(bodyPtr)
					confirmCh <- (ans == idYes)
				})
				confirmed := false
				select {
				case confirmed = <-confirmCh:
				case <-tray.shutdownCh:
				}
				if !confirmed {
					tray.postUIUpdate(func() {
						tray.toggling.Store(false)
						_ = tray.refreshIconAndTooltip()
						if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
							tray.settingsWin.updateLiveStateDisplay()
						}
					})
					return
				}
			}
		}

		changeErr := performChange(mode, ownerHwnd)
		freshState := readState(true)
		gameActive := changeErr == nil && freshState.GameRunning

		tray.postUIUpdate(func() {
			tray.toggling.Store(false)
			tray.applyFreshState(freshState)
			if changeErr != nil {
				tray.reportError("toggle-"+mode, changeErr)
				return
			}
			if gameActive {
				curStrings := getStrings(tray.settings.Language)
				tray.showBalloon(curStrings.gameRestartTitle, curStrings.gameRestartBody, niifInfo)
			}
		})
	}()
}

func (tray *residentTray) shellNotify(command uint32) error {
	result, _, callErr := procShellNotifyIconW.Call(uintptr(command), uintptr(unsafe.Pointer(&tray.iconData)))
	runtime.KeepAlive(&tray.iconData)
	if result == 0 {
		return win32CallError("Shell_NotifyIconW", callErr)
	}
	return nil
}

func (tray *residentTray) messageLoop() error {
	var message trayMessage
	for {
		for {
			r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, pmRemove)
			runtime.KeepAlive(&message)
			if r == 0 {
				break
			}
			if message.message == wmQuit {
				return nil
			}
			if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
				if handled, _, _ := procIsDialogMessageW.Call(tray.settingsWin.hwnd, uintptr(unsafe.Pointer(&message))); handled != 0 {
					continue
				}
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
		}

		tray.drainPendingUI()
		if tray.shuttingDown {
			// Drain WM_QUIT posted by tray.shutdown() cleanly before exiting.
			continue
		}

		tray.uiQueueMu.Lock()
		h := tray.uiWakeEvt
		tray.uiQueueMu.Unlock()
		if h == 0 {
			return nil
		}
		res, _, callErr := procMsgWaitForMultipleObjects.Call(
			1,
			uintptr(unsafe.Pointer(&h)),
			0,
			uintptr(windows.INFINITE),
			qsAllInput,
		)
		runtime.KeepAlive(h)
		if res == ^uintptr(0) {
			return win32CallError("MsgWaitForMultipleObjects(resident tray)", callErr)
		}
	}
}

func residentWindowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	tray := currentResidentTray
	if tray == nil || tray.hwnd != hwnd {
		result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	}
	switch message {
	case wmDisplayChange:
		tray.refreshDeviceOnlyAsync()
		return 0

	case wmDeviceChange:
		if wParam == dbtDevnodesChanged {
			tray.refreshDeviceOnlyAsync()
		}
		return 0

	case wmMeasureItem:
		if tray.onMeasureMenuItem(lParam) {
			return 1
		}

	case wmDrawItem:
		if tray.onDrawMenuItem(lParam) {
			return 1
		}

	case wmSettingChange:
		applyProcessPreferredAppMode(tray.settings.Theme)
		return 0

	case wmClose:
		if err := tray.shutdown(); err != nil {
			tray.reportError("tray-close", err)
		}
		return 0

	case wmDestroy:
		if !tray.shuttingDown {
			procPostQuitMessage.Call(0)
		}
		return 0
	}

	if message == tray.taskbarCreated {
		if err := tray.addIcon(); err != nil {
			if err.Error() != tray.lastIconErr {
				tray.lastIconErr = err.Error()
				tray.reportError("tray-recreate", err)
			}
		} else {
			tray.lastIconErr = ""
		}
		return 0
	}

	if message == tray.controlMessage {
		switch uint32(wParam) {
		case trayControlStop:
			if err := tray.shutdown(); err != nil {
				tray.reportError("tray-stop", err)
				return 0
			}
			return 1
		case trayControlActivate:
			if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
				tray.openOrFocusSettings()
			} else {
				s := getStrings(tray.settings.Language)
				tray.showBalloon(s.alreadyRunningTitle, s.alreadyRunningBody, niifInfo)
			}
			return 1
		case trayControlOpenUI:
			tray.openOrFocusSettings()
			return 1
		}
	}

	if message == tray.callbackMessage {
		action, ok := trayCallbackAction(uint32(lParam), trayIconID)
		if !ok {
			return 0
		}
		switch action {
		case "toggle":
			tray.toggleDeviceAsync(tray.hwnd)
		case "menu":
			tray.showMenu(wParam)
		}
		return 0
	}

	result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return result
}

func isHighContrastOn() bool {
	hc := highContrastInfo{cbSize: uint32(unsafe.Sizeof(highContrastInfo{}))}
	if ok, _, _ := procSystemParametersInfoW.Call(spiGetHighContrast, uintptr(hc.cbSize), uintptr(unsafe.Pointer(&hc)), 0); ok != 0 {
		return (hc.dwFlags & hcfHighContrastOn) != 0
	}
	return false
}

func (tray *residentTray) menuItemText(itemID uint32) (string, bool) {
	s := getStrings(tray.settings.Language)
	switch itemID {
	case trayMenuStatusID:
		return tray.menuStatusLine, true
	case trayMenuSettingsID:
		return s.menuSettings, true
	case trayMenuExitID:
		return s.menuExit, true
	default:
		return "", false
	}
}

func (tray *residentTray) onMeasureMenuItem(lParam uintptr) bool {
	if lParam == 0 {
		return false
	}
	var mi measureItemStruct
	copyFromWin32(unsafe.Pointer(&mi), lParam, unsafe.Sizeof(mi))
	if mi.ctlType != odtMenu {
		return false
	}
	dpi := windowDPI(tray.hwnd)
	if mi.itemID == trayMenuSepID {
		mi.itemWidth = uint32(scalePx(120, dpi))
		mi.itemHeight = uint32(scalePx(9, dpi))
		copyToWin32(lParam, unsafe.Pointer(&mi), unsafe.Sizeof(mi))
		return true
	}
	label, ok := tray.menuItemText(mi.itemID)
	if !ok {
		return false
	}
	dc, _, _ := procGetDC.Call(tray.hwnd)
	if dc == 0 {
		return false
	}
	defer procReleaseDC.Call(tray.hwnd, dc)
	var oldFont uintptr
	if tray.menuFont != 0 {
		oldFont, _, _ = procSelectObject.Call(dc, tray.menuFont)
	}
	u16, _ := windows.UTF16FromString(label)
	charLen := len(u16)
	if charLen > 0 {
		charLen--
	}
	var extent traySize
	if charLen > 0 {
		procGetTextExtentPoint32W.Call(dc, uintptr(unsafe.Pointer(&u16[0])), uintptr(charLen), uintptr(unsafe.Pointer(&extent)))
	}
	runtime.KeepAlive(u16)
	if oldFont != 0 {
		procSelectObject.Call(dc, oldFont)
	}
	w := extent.cx + scalePx(36, dpi)
	if w < scalePx(170, dpi) {
		w = scalePx(170, dpi)
	}
	h := extent.cy + scalePx(12, dpi)
	if h < scalePx(30, dpi) {
		h = scalePx(30, dpi)
	}
	mi.itemWidth = uint32(w)
	mi.itemHeight = uint32(h)
	copyToWin32(lParam, unsafe.Pointer(&mi), unsafe.Sizeof(mi))
	return true
}

func (tray *residentTray) onDrawMenuItem(lParam uintptr) bool {
	if lParam == 0 {
		return false
	}
	var di drawItemStruct
	copyFromWin32(unsafe.Pointer(&di), lParam, unsafe.Sizeof(di))
	if di.ctlType != odtMenu {
		return false
	}
	dpi := windowDPI(tray.hwnd)
	hc := isHighContrastOn()
	selected := (di.itemState & odsSelected) != 0
	disabled := (di.itemState&(odsDisabled|odsGrayed)) != 0 || di.itemID == trayMenuStatusID

	var bg, fg, sepCol uint32
	if hc {
		if selected && !disabled {
			r, _, _ := procGetSysColor.Call(colorHighlight)
			bg = uint32(r)
			r2, _, _ := procGetSysColor.Call(colorHighlightText)
			fg = uint32(r2)
		} else {
			r, _, _ := procGetSysColor.Call(colorMenu)
			bg = uint32(r)
			if disabled {
				r2, _, _ := procGetSysColor.Call(colorGrayText)
				fg = uint32(r2)
			} else {
				r2, _, _ := procGetSysColor.Call(colorMenuText)
				fg = uint32(r2)
			}
		}
		sepCol = fg
	} else if tray.menuDark {
		if selected && !disabled {
			bg = colorRef(58, 58, 62)
		} else {
			bg = colorRef(32, 32, 35)
		}
		if disabled {
			fg = colorRef(155, 155, 164)
		} else {
			fg = colorRef(244, 244, 245)
		}
		sepCol = colorRef(62, 62, 68)
	} else {
		if selected && !disabled {
			bg = colorRef(229, 231, 235)
		} else {
			bg = colorRef(250, 250, 250)
		}
		if disabled {
			fg = colorRef(107, 114, 128)
		} else {
			fg = colorRef(24, 24, 27)
		}
		sepCol = colorRef(226, 228, 233)
	}

	bgBrush, _, _ := procCreateSolidBrush.Call(uintptr(bg))
	if bgBrush != 0 {
		procFillRect.Call(di.hDC, uintptr(unsafe.Pointer(&di.rcItem)), bgBrush)
		procDeleteObject.Call(bgBrush)
	}

	if di.itemID == trayMenuSepID {
		midY := (di.rcItem.top + di.rcItem.bottom) / 2
		sepRect := winRect{
			left:   di.rcItem.left + scalePx(12, dpi),
			top:    midY,
			right:  di.rcItem.right - scalePx(12, dpi),
			bottom: midY + 1,
		}
		sepBrush, _, _ := procCreateSolidBrush.Call(uintptr(sepCol))
		if sepBrush != 0 {
			procFillRect.Call(di.hDC, uintptr(unsafe.Pointer(&sepRect)), sepBrush)
			procDeleteObject.Call(sepBrush)
		}
		return true
	}

	label, ok := tray.menuItemText(di.itemID)
	if !ok {
		return false
	}
	var oldFont uintptr
	if tray.menuFont != 0 {
		oldFont, _, _ = procSelectObject.Call(di.hDC, tray.menuFont)
	}
	procSetBkMode.Call(di.hDC, 1) // TRANSPARENT
	procSetTextColor.Call(di.hDC, uintptr(fg))

	txtRect := di.rcItem
	txtRect.left += scalePx(16, dpi)
	txtRect.right -= scalePx(14, dpi)
	ptr, _ := windows.UTF16PtrFromString(label)
	negOne := ^uintptr(0)
	procDrawTextW.Call(di.hDC, uintptr(unsafe.Pointer(ptr)), negOne, uintptr(unsafe.Pointer(&txtRect)), dtSingleLine|dtVCenter|dtLeft|dtNoPrefix)
	runtime.KeepAlive(ptr)
	if oldFont != 0 {
		procSelectObject.Call(di.hDC, oldFont)
	}
	return true
}

func (tray *residentTray) buildMenuStatusSummary() string {
	s := getStrings(tray.settings.Language)
	var dev string
	if tray.isToggling() {
		dev = s.deviceSwitching
	} else if tray.cachedState.Status == "enabled" {
		dev = s.deviceEnabled
	} else if tray.cachedState.Status == "disabled" {
		dev = s.deviceDisabled
	} else {
		dev = s.deviceUnavailable
	}
	mon := s.monitorStopped
	if tray.cachedState.WatcherStatus == "error" {
		mon = s.monitorError
	} else if tray.cachedState.WatcherActive {
		mon = s.deviceEnabled
	}
	return fmt.Sprintf(s.menuStatusFmt, dev, mon)
}

func (tray *residentTray) showMenu(anchor uintptr) {
	if tray.menuOpen || tray.shuttingDown {
		return
	}
	tray.menuOpen = true
	defer func() { tray.menuOpen = false }()

	// Refresh lightweight device & monitor heartbeat right as menu opens so summary is up-to-date.
	if !tray.isToggling() {
		tray.applyDeviceState(readDeviceOnlyState())
	}
	if w, err := readHeartbeatForStatus(); err != nil {
		tray.cachedState.WatcherActive = false
		tray.cachedState.WatcherStatus = "error"
		tray.cachedState.WatcherError = err.Error()
	} else if w != nil && w.Status != "stopped" {
		tray.cachedState.WatcherActive = true
		tray.cachedState.WatcherStatus = w.Status
		tray.cachedState.WatcherError = w.Error
	} else {
		tray.cachedState.WatcherActive = false
		tray.cachedState.WatcherStatus = "stopped"
		tray.cachedState.WatcherError = ""
	}
	_ = tray.refreshIconAndTooltip()

	tray.menuDark = effectiveDarkTheme(tray.settings.Theme)
	applyProcessPreferredAppMode(tray.settings.Theme)
	tray.menuStatusLine = tray.buildMenuStatusSummary()

	dpi := windowDPI(tray.hwnd)
	tray.menuFont = createUIFont(9, 400, dpi, tray.settings.Language)
	if tray.menuFont != 0 {
		defer func() {
			procDeleteObject.Call(tray.menuFont)
			tray.menuFont = 0
		}()
	}

	menu, _, callErr := procCreatePopupMenu.Call()
	if menu == 0 {
		tray.reportError("tray-menu", win32CallError("CreatePopupMenu", callErr))
		return
	}
	defer procDestroyMenu.Call(menu)

	procAppendMenuW.Call(menu, mfOwnerDraw|mfDisabled|mfGrayed, trayMenuStatusID, 0)
	procAppendMenuW.Call(menu, mfOwnerDraw|mfSeparator, trayMenuSepID, 0)
	procAppendMenuW.Call(menu, mfOwnerDraw, trayMenuSettingsID, 0)
	procAppendMenuW.Call(menu, mfOwnerDraw, trayMenuExitID, 0)

	x, y := decodeMenuAnchor(anchor)
	if menuAnchorNeedsCursorPos(x, y) {
		var point trayPoint
		if result, _, callErr := procGetCursorPos.Call(uintptr(unsafe.Pointer(&point))); result == 0 {
			tray.reportError("tray-menu", win32CallError("GetCursorPos", callErr))
			return
		}
		x, y = point.x, point.y
	}
	procSetForegroundWindow.Call(tray.hwnd)
	command, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCommand, uintptr(x), uintptr(y), 0, tray.hwnd, 0)
	procPostMessageW.Call(tray.hwnd, wmNull, 0, 0)
	if command == 0 {
		return
	}
	switch command {
	case trayMenuSettingsID:
		tray.openOrFocusSettings()
	case trayMenuExitID:
		tray.exitFromMenu()
	}
}

func menuAnchorNeedsCursorPos(x, y int32) bool {
	return (x == -32768 && y == -32768) || (x == -1 && y == -1)
}

func decodeMenuAnchor(anchor uintptr) (int32, int32) {
	packed := uint32(anchor)
	x := int32(int16(packed & 0xffff))
	y := int32(int16((packed >> 16) & 0xffff))
	return x, y
}

func (tray *residentTray) exitFromMenu() {
	if tray.isToggling() {
		s := getStrings(tray.settings.Language)
		tray.showBalloon(s.errorTitle, s.deviceSwitching, niifInfo)
		return
	}
	if !globalReadOnly {
		if err := stopUUWatch(); err != nil {
			tray.reportError("tray-exit", err)
			return
		}
	}
	if err := tray.shutdown(); err != nil {
		tray.reportError("tray-exit", err)
	}
}

func (tray *residentTray) reportError(mode string, err error) {
	if err == nil {
		return
	}
	s := getStrings(tray.settings.Language)
	message := err.Error()
	if !globalReadOnly {
		if logErr := appendActivity(activity{Time: time.Now().Format(time.RFC3339), Mode: mode, Message: message}); logErr != nil {
			message += "\nCould not save the error log: " + logErr.Error()
		}
	}
	// When the Settings window is visible, show a modal error dialog; otherwise prefer
	// a non-blocking tray notification balloon so background events do not hijack focus.
	if tray.settingsWin == nil || tray.settingsWin.hwnd == 0 {
		if tray.iconAdded && !globalReadOnly {
			tray.showBalloon(s.errorTitle, message, niifError)
			return
		}
	}
	if globalReadOnly || tray.reportingError {
		return
	}
	tray.reportingError = true
	defer func() { tray.reportingError = false }()
	owner := tray.hwnd
	if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
		owner = tray.settingsWin.hwnd
	}
	title, _ := windows.UTF16PtrFromString(s.errorTitle)
	body, _ := windows.UTF16PtrFromString(message)
	procMessageBoxW.Call(owner, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), mbIconError|mbTopMost)
	runtime.KeepAlive(title)
	runtime.KeepAlive(body)
}

func (tray *residentTray) shutdown() error {
	if tray.shuttingDown {
		return nil
	}
	if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
		procDestroyWindow.Call(tray.settingsWin.hwnd)
		tray.settingsWin = nil
	}
	if tray.readySignaled {
		if result, _, callErr := procResetEvent.Call(uintptr(tray.readyEvent)); result == 0 {
			return win32CallError("ResetEvent(tray readiness)", callErr)
		}
		tray.readySignaled = false
	}
	if tray.iconAdded {
		if err := tray.shellNotify(nimDelete); err != nil {
			result, _, callErr := procSetEvent.Call(uintptr(tray.readyEvent))
			if result != 0 {
				tray.readySignaled = true
				return err
			}
			restoreErr := win32CallError("SetEvent(tray readiness)", callErr)
			return fmt.Errorf("%w; could not restore tray readiness: %v", err, restoreErr)
		}
		tray.iconAdded = false
	}
	tray.uiQueueMu.Lock()
	tray.shuttingDown = true
	tray.uiQueueMu.Unlock()
	if tray.shutdownCh != nil {
		tray.shutdownOnce.Do(func() { close(tray.shutdownCh) })
	}
	if tray.menuOpen {
		procEndMenu.Call()
	}
	procPostQuitMessage.Call(0)
	return nil
}

func (tray *residentTray) cleanup() {
	if tray.monitorEvtStop != 0 {
		procSetEvent.Call(uintptr(tray.monitorEvtStop))
		if tray.monitorEvtDone != nil {
			<-tray.monitorEvtDone
			tray.monitorEvtDone = nil
		}
		_ = windows.CloseHandle(tray.monitorEvtStop)
		tray.monitorEvtStop = 0
	}
	if tray.devSubscription != nil {
		tray.devSubscription.Close()
		tray.devSubscription = nil
	}
	stopInProcessMonitor()

	if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
		procDestroyWindow.Call(tray.settingsWin.hwnd)
		tray.settingsWin = nil
	}
	if tray.iconAdded {
		if err := tray.shellNotify(nimDelete); err != nil {
			tray.reportError("tray-cleanup", err)
		}
		tray.iconAdded = false
	}
	if tray.readySignaled {
		procResetEvent.Call(uintptr(tray.readyEvent))
		tray.readySignaled = false
	}
	tray.icons.Destroy()
	tray.uiQueueMu.Lock()
	tray.shuttingDown = true
	tray.uiQueue = nil
	wakeEvt := tray.uiWakeEvt
	tray.uiWakeEvt = 0
	tray.uiQueueMu.Unlock()
	if tray.shutdownCh != nil {
		tray.shutdownOnce.Do(func() { close(tray.shutdownCh) })
	}
	if tray.hwnd != 0 {
		procDestroyWindow.Call(tray.hwnd)
		tray.hwnd = 0
	}
	var msg trayMessage
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, wmQuit, wmQuit, pmRemove)
		runtime.KeepAlive(&msg)
		if r == 0 {
			break
		}
	}
	if wakeEvt != 0 {
		_ = windows.CloseHandle(wakeEvt)
	}
	currentResidentTray = nil
}

func stopResidentTray() error {
	hwnd, err := findResidentWindow()
	if err != nil {
		return err
	}
	mutexName, err := windows.UTF16PtrFromString(trayMutexFullName())
	if err != nil {
		return err
	}
	mutex, mutexErr := windows.OpenMutex(windows.SYNCHRONIZE, false, mutexName)
	if mutexErr != nil && !errors.Is(mutexErr, windows.ERROR_FILE_NOT_FOUND) {
		return fmt.Errorf("Could not verify resident tray mutex: %w", mutexErr)
	}
	if mutexErr == nil {
		defer windows.CloseHandle(mutex)
	}
	eventName, err := windows.UTF16PtrFromString(trayReadyEventFullName())
	if err != nil {
		return err
	}
	event, eventErr := windows.OpenEvent(windows.SYNCHRONIZE, false, eventName)
	if eventErr != nil && !errors.Is(eventErr, windows.ERROR_FILE_NOT_FOUND) {
		return fmt.Errorf("Could not verify resident tray readiness: %w", eventErr)
	}
	if eventErr == nil {
		defer windows.CloseHandle(event)
	}
	if hwnd == 0 {
		if mutexErr != nil && eventErr != nil {
			return nil
		}
		return errors.New("A resident tray object exists, but its window could not be verified.")
	}
	pid, image, err := residentImage(hwnd)
	if err != nil {
		return err
	}
	core, err := trayCorePath()
	if err != nil {
		return err
	}
	if !sameTrayPath(core, image) {
		return fmt.Errorf("Refusing to stop a tray resident from another application path: %s", image)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|processQueryLimitedInformation, false, pid)
	if err != nil {
		return fmt.Errorf("Could not verify resident tray process %d: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	message, err := registerTrayMessage(trayControlMessage)
	if err != nil {
		return err
	}
	var response uintptr
	result, _, callErr := procSendMessageTimeoutW.Call(
		hwnd, uintptr(message), uintptr(trayControlStop), 0,
		smtoBlock|smtoAbortIfHung, 3000, uintptr(unsafe.Pointer(&response)),
	)
	if result == 0 {
		return win32CallError("SendMessageTimeoutW(stop tray)", callErr)
	}
	runtime.KeepAlive(&response)
	if response != 1 {
		return errors.New("The resident tray did not confirm icon removal.")
	}
	waitResult, err := windows.WaitForSingleObject(process, 10000)
	if err != nil {
		return fmt.Errorf("Could not confirm resident tray exit: %w", err)
	}
	if waitResult == waitTimeout {
		return errors.New("The resident tray did not exit after confirming icon removal.")
	}
	if waitResult != waitObject0 {
		return fmt.Errorf("Unexpected resident tray exit result %d", waitResult)
	}
	if status := trayStatusNow(); status.Ready {
		return errors.New("A resident tray is still ready after stop confirmation.")
	} else if status.Message != "" {
		return errors.New(status.Message)
	}
	return nil
}

func toUTF16Array(value string, size int) []uint16 {
	encoded, _ := windows.UTF16FromString(value)
	if len(encoded) > size {
		encoded = encoded[:size]
		encoded[len(encoded)-1] = 0
	}
	return encoded
}

func mainTrayCommand(showStatus, start, stop, openSettings bool) error {
	if showStatus {
		return writeStdoutJSON(trayStatusNow())
	}
	if start {
		return runResidentTray(openSettings, nil)
	}
	if stop {
		return stopResidentTray()
	}
	return nil
}
