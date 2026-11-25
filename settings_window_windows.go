package main

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dwmapi  = windows.NewLazySystemDLL("dwmapi.dll")
	uxtheme = windows.NewLazySystemDLL("uxtheme.dll")

	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procSetWindowTheme        = uxtheme.NewProc("SetWindowTheme")
	procSetPreferredAppMode   = uxtheme.NewProc("#135")
	procFlushMenuThemes       = uxtheme.NewProc("#136")

	procCreateFontW          = gdi32.NewProc("CreateFontW")
	procCreateSolidBrush     = gdi32.NewProc("CreateSolidBrush")
	procCreatePen            = gdi32.NewProc("CreatePen")
	procRoundRect            = gdi32.NewProc("RoundRect")
	procSetTextColor         = gdi32.NewProc("SetTextColor")
	procSetBkColor           = gdi32.NewProc("SetBkColor")
	procSetBkMode            = gdi32.NewProc("SetBkMode")
	procFillRect             = user32Tray.NewProc("FillRect")
	procBeginPaint           = user32Tray.NewProc("BeginPaint")
	procEndPaint             = user32Tray.NewProc("EndPaint")
	procInvalidateRect       = user32Tray.NewProc("InvalidateRect")
	procGetClientRect        = user32Tray.NewProc("GetClientRect")
	procAdjustWindowRectExDpi = user32Tray.NewProc("AdjustWindowRectExForDpi")
	procSetWindowPos         = user32Tray.NewProc("SetWindowPos")
	procSetWindowTextW       = user32Tray.NewProc("SetWindowTextW")
	procSendMessageW         = user32Tray.NewProc("SendMessageW")
	procEnableWindow         = user32Tray.NewProc("EnableWindow")
	procIsWindow             = user32Tray.NewProc("IsWindow")
	procLoadCursorW          = user32Tray.NewProc("LoadCursorW")
	procMonitorFromPoint     = user32Tray.NewProc("MonitorFromPoint")
	procGetMonitorInfoW      = user32Tray.NewProc("GetMonitorInfoW")
	procIsDialogMessageW     = user32Tray.NewProc("IsDialogMessageW")

	settingsWndProcPtr      = windows.NewCallback(settingsWindowProc)
	settingsClassOnce       sync.Once
	settingsClassRegisterErr error
)

const (
	settingsWindowBaseClass = "UUDisplaySwitch.Settings.Window.v1"

	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsVisible      = 0x10000000
	wsChild        = 0x40000000
	wsTabStop      = 0x00010000
	wsClipChildren = 0x02000000

	bsPushBtn   = 0x00000000
	bsAutoCheck = 0x00000003
	cbsDropList = 0x00000003

	swShow    = 5
	swRestore = 9

	wmCreate          = 0x0001
	wmSetFont         = 0x0030
	wmGetIcon         = 0x007F
	wmSetIcon         = 0x0080
	wmNcDestroy       = 0x0082
	wmCommand         = 0x0111
	wmCtlColorListBox = 0x0134
	wmCtlColorBtn     = 0x0135
	wmCtlColorStatic  = 0x0138
	wmPaint           = 0x000F
	wmEraseBkgnd      = 0x0014
	wmLButtonUp       = 0x0202
	wmDpiChanged      = 0x02E0
	wmSettingChange   = 0x001A
	wmDisplayChange   = 0x007E

	dtWordBreak   = 0x00000010
	dtEndEllipsis = 0x00008000

	colorWindow     = 5
	colorWindowText = 8

	bmGetCheck   = 0x00F0
	bmSetCheck   = 0x00F1
	bstUnchecked = 0
	bstChecked   = 1

	cbAddString    = 0x0143
	cbResetContent = 0x014B
	cbGetCurSel    = 0x0147
	cbSetCurSel    = 0x014E
	cbnSelChange   = 1
	bnClicked      = 0

	dwmwaUseImmersiveDarkMode = 20
	dwmwaWindowCornerPref     = 33
	dwmwcpRound               = 2

	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	idcArrow                = 32512
	monitorDefaultToNearest = 2

	idBtnToggle      = 1001
	idBtnRefresh     = 1002
	idChkAutoDisable = 1003
	idChkAutoExit    = 1005
	idChkStartup     = 1007
	idCmbTheme       = 1009
	idCmbLanguage    = 1010
	idBtnClose       = 2 // IDCANCEL so Esc closes cleanly
)

type winRect struct {
	left, top, right, bottom int32
}

type paintStruct struct {
	hdc         uintptr
	fErase      int32
	rcPaint     winRect
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

type monitorInfo struct {
	cbSize    uint32
	rcMonitor winRect
	rcWork    winRect
	dwFlags   uint32
}

type settingsWindow struct {
	tray         *residentTray
	hwnd         uintptr
	dpi          uint32
	hc           bool
	dark         bool
	updatingUI   bool
	startupKnown bool
	startupOn    bool
	startupBusy  bool
	autoBusy     bool

	fontHeading uintptr
	fontBold    uintptr
	fontBody    uintptr
	fontHint    uintptr
	bgBrush     uintptr
	cardBrush   uintptr
	smallIcon   uintptr
	bigIcon     uintptr

	cardRects [3]winRect

	// Painted label rects (zero STATIC child HWNDs)
	rcHdrDevice       winRect
	rcStatusTitle     winRect
	rcDeviceSub       winRect
	rcMonitorState    winRect
	rcHdrAuto         winRect
	rcLblAutoDisable  winRect
	rcHintAutoDisable winRect
	rcLblAutoExit     winRect
	rcHintAutoExit    winRect
	rcLblStartup      winRect
	rcHintStartup     winRect
	rcHdrAppearance   winRect
	rcLblTheme        winRect
	rcLblLanguage     winRect
	rcFooter          winRect

	// Interactive child control HWNDs (only 8 controls)
	btnToggle      uintptr
	btnRefresh     uintptr
	chkAutoDisable uintptr
	chkAutoExit    uintptr
	chkStartup     uintptr
	cmbTheme       uintptr
	cmbLanguage    uintptr
	btnClose       uintptr
}

func packPoint64(x, y int32) uintptr {
	return uintptr(uint32(x)) | (uintptr(uint32(y)) << 32)
}

func ptInRect(rc winRect, x, y int32) bool {
	return x >= rc.left && x < rc.right && y >= rc.top && y < rc.bottom
}

func colorRef(r, g, b byte) uint32 {
	return uint32(r) | (uint32(g) << 8) | (uint32(b) << 16)
}

func applyProcessPreferredAppMode(theme string) {
	if procSetPreferredAppMode.Find() == nil {
		var mode uintptr = 1 // AllowDark (follow system)
		switch theme {
		case "dark":
			mode = 2 // ForceDark
		case "light":
			mode = 3 // ForceLight
		}
		procSetPreferredAppMode.Call(mode)
	}
	if procFlushMenuThemes.Find() == nil {
		procFlushMenuThemes.Call()
	}
}

func scalePx(px int32, dpi uint32) int32 {
	return int32((int64(px)*int64(dpi) + 48) / 96)
}

func createUIFont(pt int32, weight int32, dpi uint32, lang string) uintptr {
	height := -int32((int64(pt)*int64(dpi) + 36) / 72)
	face := "Microsoft YaHei UI"
	if lang == "zh-TW" {
		face = "Microsoft JhengHei UI"
	} else if lang == "en" {
		face = "Segoe UI"
	}
	facePtr, _ := windows.UTF16PtrFromString(face)
	hFont, _, _ := procCreateFontW.Call(
		uintptr(height), 0, 0, 0,
		uintptr(weight),
		0, 0, 0,
		1, // DEFAULT_CHARSET
		0, 0,
		5, // CLEARTYPE_QUALITY
		0,
		uintptr(unsafe.Pointer(facePtr)),
	)
	runtime.KeepAlive(facePtr)
	return hFont
}

func ensureSettingsClassRegistered() error {
	settingsClassOnce.Do(func() {
		clsName, err := windows.UTF16PtrFromString(windowClassForNamespace(settingsWindowBaseClass))
		if err != nil {
			settingsClassRegisterErr = err
			return
		}
		module, _, modErr := procGetModuleHandleW.Call(0)
		if module == 0 {
			settingsClassRegisterErr = win32CallError("GetModuleHandleW", modErr)
			return
		}
		cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
		wc := trayWindowClass{
			cbSize:        uint32(unsafe.Sizeof(trayWindowClass{})),
			lpfnWndProc:   settingsWndProcPtr,
			hInstance:     module,
			hCursor:       cursor,
			lpszClassName: clsName,
		}
		reg, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		runtime.KeepAlive(clsName)
		runtime.KeepAlive(&wc)
		if reg == 0 {
			settingsClassRegisterErr = win32CallError("RegisterClassExW(settings)", callErr)
		}
	})
	return settingsClassRegisterErr
}

func (tray *residentTray) openOrFocusSettings() {
	if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
		if ok, _, _ := procIsWindow.Call(tray.settingsWin.hwnd); ok != 0 {
			procShowWindow.Call(tray.settingsWin.hwnd, swRestore)
			procSetForegroundWindow.Call(tray.settingsWin.hwnd)
			tray.settingsWin.refreshStateAsync(false)
			return
		}
		tray.settingsWin = nil
	}
	if err := ensureSettingsClassRegistered(); err != nil {
		tray.reportError("settings-class", err)
		return
	}

	var cursor trayPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	hMon, _, _ := procMonitorFromPoint.Call(packPoint64(cursor.x, cursor.y), monitorDefaultToNearest)
	mi := monitorInfo{cbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	work := winRect{left: 0, top: 0, right: 1920, bottom: 1080}
	if hMon != 0 {
		if ok, _, _ := procGetMonitorInfoW.Call(hMon, uintptr(unsafe.Pointer(&mi))); ok != 0 {
			work = mi.rcWork
		}
	}

	dpi := systemDPI()
	style := uint32(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox | wsClipChildren)
	clientRect := winRect{left: 0, top: 0, right: scalePx(468, dpi), bottom: scalePx(518, dpi)}
	if procAdjustWindowRectExDpi.Find() == nil {
		procAdjustWindowRectExDpi.Call(uintptr(unsafe.Pointer(&clientRect)), uintptr(style), 0, 0, uintptr(dpi))
	}
	winW := clientRect.right - clientRect.left
	winH := clientRect.bottom - clientRect.top
	posX := work.left + ((work.right-work.left)-winW)/2
	posY := work.top + ((work.bottom-work.top)-winH)/2

	hc := isHighContrastOn()
	sw := &settingsWindow{
		tray:         tray,
		dpi:          dpi,
		hc:           hc,
		dark:         !hc && effectiveDarkTheme(tray.settings.Theme),
		startupKnown: tray.startupKnown,
		startupOn:    tray.startupOn,
	}
	tray.settingsWin = sw

	clsName, _ := windows.UTF16PtrFromString(windowClassForNamespace(settingsWindowBaseClass))
	titlePtr, _ := windows.UTF16PtrFromString(getStrings(tray.settings.Language).settingsTitle)
	module, _, _ := procGetModuleHandleW.Call(0)

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(style),
		uintptr(posX), uintptr(posY), uintptr(winW), uintptr(winH),
		0, 0, module, 0,
	)
	runtime.KeepAlive(clsName)
	runtime.KeepAlive(titlePtr)
	if hwnd == 0 {
		tray.settingsWin = nil
		tray.reportError("settings-window", win32CallError("CreateWindowExW(settings)", callErr))
		return
	}
	sw.hwnd = hwnd
	sw.dpi = windowDPI(hwnd)
	sw.smallIcon = loadEmbeddedIconAtSize(smallIconSizeForDPI(sw.dpi))
	sw.bigIcon = loadEmbeddedIconAtSize(largeIconSizeForDPI(sw.dpi))
	if sw.smallIcon != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, 0, sw.smallIcon)
	}
	if sw.bigIcon != 0 {
		procSendMessageW.Call(hwnd, wmSetIcon, 1, sw.bigIcon)
	}

	sw.createChildren()
	sw.rebuildThemeAndFonts()
	sw.populateTextsAndValues()
	sw.layoutChildren()

	// Resize accurately if window monitor DPI differs from system DPI.
	actualClient := winRect{right: scalePx(468, sw.dpi), bottom: scalePx(518, sw.dpi)}
	if procAdjustWindowRectExDpi.Find() == nil {
		procAdjustWindowRectExDpi.Call(uintptr(unsafe.Pointer(&actualClient)), uintptr(style), 0, 0, uintptr(sw.dpi))
		actualW := actualClient.right - actualClient.left
		actualH := actualClient.bottom - actualClient.top
		if actualW != winW || actualH != winH {
			nx := work.left + ((work.right-work.left)-actualW)/2
			ny := work.top + ((work.bottom-work.top)-actualH)/2
			procSetWindowPos.Call(hwnd, 0, uintptr(nx), uintptr(ny), uintptr(actualW), uintptr(actualH), swpNoZOrder|swpNoActivate)
		}
	}

	procShowWindow.Call(hwnd, swShow)
	procSetForegroundWindow.Call(hwnd)
	sw.refreshStateAsync(!tray.startupKnown)
}

func (sw *settingsWindow) addChild(className, text string, style uint32, id uintptr) uintptr {
	clsPtr, _ := windows.UTF16PtrFromString(className)
	txtPtr, _ := windows.UTF16PtrFromString(text)
	module, _, _ := procGetModuleHandleW.Call(0)
	h, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsPtr)),
		uintptr(unsafe.Pointer(txtPtr)),
		uintptr(wsChild|wsVisible|style),
		0, 0, 10, 10,
		sw.hwnd,
		id,
		module,
		0,
	)
	runtime.KeepAlive(clsPtr)
	runtime.KeepAlive(txtPtr)
	return h
}

func (sw *settingsWindow) createChildren() {
	sw.btnToggle = sw.addChild("BUTTON", "", bsPushBtn|wsTabStop, idBtnToggle)
	sw.btnRefresh = sw.addChild("BUTTON", "", bsPushBtn|wsTabStop, idBtnRefresh)
	sw.chkAutoDisable = sw.addChild("BUTTON", "", bsAutoCheck|wsTabStop, idChkAutoDisable)
	sw.chkAutoExit = sw.addChild("BUTTON", "", bsAutoCheck|wsTabStop, idChkAutoExit)
	sw.chkStartup = sw.addChild("BUTTON", "", bsAutoCheck|wsTabStop, idChkStartup)
	sw.cmbTheme = sw.addChild("COMBOBOX", "", cbsDropList|wsTabStop, idCmbTheme)
	sw.cmbLanguage = sw.addChild("COMBOBOX", "", cbsDropList|wsTabStop, idCmbLanguage)
	sw.btnClose = sw.addChild("BUTTON", "", bsPushBtn|wsTabStop, idBtnClose)
}

func setCtrlText(hwnd uintptr, text string) {
	if hwnd == 0 {
		return
	}
	p, _ := windows.UTF16PtrFromString(text)
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}

func setCtrlFont(hwnd, font uintptr) {
	if hwnd != 0 && font != 0 {
		procSendMessageW.Call(hwnd, wmSetFont, font, 1)
	}
}

func (sw *settingsWindow) freeGDIResources() {
	for _, obj := range []*uintptr{&sw.fontHeading, &sw.fontBold, &sw.fontBody, &sw.fontHint, &sw.bgBrush, &sw.cardBrush} {
		if *obj != 0 {
			procDeleteObject.Call(*obj)
			*obj = 0
		}
	}
}

func (sw *settingsWindow) destroyIcons() {
	for _, ic := range []*uintptr{&sw.smallIcon, &sw.bigIcon} {
		if *ic != 0 {
			procDestroyIcon.Call(*ic)
			*ic = 0
		}
	}
}

func sysColor(index int32) uint32 {
	r, _, _ := procGetSysColor.Call(uintptr(index))
	return uint32(r)
}

func (sw *settingsWindow) rebuildThemeAndFonts() {
	sw.freeGDIResources()
	cfg := sw.tray.settings
	sw.hc = isHighContrastOn()
	sw.dark = !sw.hc && effectiveDarkTheme(cfg.Theme)
	applyProcessPreferredAppMode(cfg.Theme)

	var darkFlag int32
	if sw.dark {
		darkFlag = 1
	}
	if procDwmSetWindowAttribute.Find() == nil {
		procDwmSetWindowAttribute.Call(sw.hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&darkFlag)), 4)
		corner := int32(dwmwcpRound)
		procDwmSetWindowAttribute.Call(sw.hwnd, dwmwaWindowCornerPref, uintptr(unsafe.Pointer(&corner)), 4)
	}

	if sw.hc {
		hcBg := uintptr(sysColor(colorWindow))
		sw.bgBrush, _, _ = procCreateSolidBrush.Call(hcBg)
		sw.cardBrush, _, _ = procCreateSolidBrush.Call(hcBg)
	} else if sw.dark {
		sw.bgBrush, _, _ = procCreateSolidBrush.Call(uintptr(colorRef(24, 24, 27)))
		sw.cardBrush, _, _ = procCreateSolidBrush.Call(uintptr(colorRef(39, 39, 42)))
	} else {
		sw.bgBrush, _, _ = procCreateSolidBrush.Call(uintptr(colorRef(243, 244, 246)))
		sw.cardBrush, _, _ = procCreateSolidBrush.Call(uintptr(colorRef(255, 255, 255)))
	}

	sw.fontHeading = createUIFont(11, 600, sw.dpi, cfg.Language)
	sw.fontBold = createUIFont(10, 600, sw.dpi, cfg.Language)
	sw.fontBody = createUIFont(9, 400, sw.dpi, cfg.Language)
	sw.fontHint = createUIFont(8, 400, sw.dpi, cfg.Language)

	for _, h := range []uintptr{sw.cmbTheme, sw.cmbLanguage, sw.btnToggle, sw.btnRefresh, sw.btnClose} {
		setCtrlFont(h, sw.fontBody)
	}

	if procSetWindowTheme.Find() == nil {
		btnTheme := "Explorer"
		cmbTheme := "Explorer"
		if sw.hc {
			btnTheme = ""
			cmbTheme = ""
		} else if sw.dark {
			btnTheme = "DarkMode_Explorer"
			cmbTheme = "DarkMode_CFD"
		}
		btnPtr, _ := windows.UTF16PtrFromString(btnTheme)
		for _, ctrl := range []uintptr{sw.btnToggle, sw.btnRefresh, sw.btnClose, sw.chkAutoDisable, sw.chkAutoExit, sw.chkStartup} {
			procSetWindowTheme.Call(ctrl, uintptr(unsafe.Pointer(btnPtr)), 0)
		}
		runtime.KeepAlive(btnPtr)

		cmbPtr, _ := windows.UTF16PtrFromString(cmbTheme)
		for _, ctrl := range []uintptr{sw.cmbTheme, sw.cmbLanguage} {
			procSetWindowTheme.Call(ctrl, uintptr(unsafe.Pointer(cmbPtr)), 0)
		}
		runtime.KeepAlive(cmbPtr)
	}

	procInvalidateRect.Call(sw.hwnd, 0, 0)
}

func (sw *settingsWindow) populateTextsAndValues() {
	sw.updatingUI = true
	defer func() { sw.updatingUI = false }()

	cfg := sw.tray.settings
	s := getStrings(cfg.Language)

	setCtrlText(sw.hwnd, s.settingsTitle)
	setCtrlText(sw.btnRefresh, s.btnRefresh)
	setCtrlText(sw.btnClose, s.btnClose)

	// Checkboxes
	setChecked(sw.chkAutoDisable, cfg.AutoDisable)
	setChecked(sw.chkAutoExit, cfg.AutoExit)
	setChecked(sw.chkStartup, sw.startupOn)

	// Theme ComboBox
	procSendMessageW.Call(sw.cmbTheme, cbResetContent, 0, 0)
	for _, item := range []string{s.themeSystem, s.themeLight, s.themeDark} {
		p, _ := windows.UTF16PtrFromString(item)
		procSendMessageW.Call(sw.cmbTheme, cbAddString, 0, uintptr(unsafe.Pointer(p)))
		runtime.KeepAlive(p)
	}
	themeIdx := 0
	if cfg.Theme == "light" {
		themeIdx = 1
	} else if cfg.Theme == "dark" {
		themeIdx = 2
	}
	procSendMessageW.Call(sw.cmbTheme, cbSetCurSel, uintptr(themeIdx), 0)

	// Language ComboBox
	procSendMessageW.Call(sw.cmbLanguage, cbResetContent, 0, 0)
	for _, item := range []string{"简体中文", "繁體中文", "English"} {
		p, _ := windows.UTF16PtrFromString(item)
		procSendMessageW.Call(sw.cmbLanguage, cbAddString, 0, uintptr(unsafe.Pointer(p)))
		runtime.KeepAlive(p)
	}
	langIdx := 0
	if cfg.Language == "zh-TW" {
		langIdx = 1
	} else if cfg.Language == "en" {
		langIdx = 2
	}
	procSendMessageW.Call(sw.cmbLanguage, cbSetCurSel, uintptr(langIdx), 0)

	sw.updateLiveStateDisplay()
}

func (sw *settingsWindow) updateLiveStateDisplay() {
	if sw == nil || sw.hwnd == 0 {
		return
	}
	cfg := sw.tray.settings
	s := getStrings(cfg.Language)
	st := sw.tray.cachedState
	busy := sw.tray.isToggling()

	if st.Status == "enabled" {
		setCtrlText(sw.btnToggle, s.btnDisable)
	} else {
		setCtrlText(sw.btnToggle, s.btnEnable)
	}

	canMutate := !globalReadOnly && !busy && (st.Status == "enabled" || st.Status == "disabled")
	enableCtrl(sw.btnToggle, canMutate)
	enableCtrl(sw.btnRefresh, !busy)
	enableCtrl(sw.chkAutoDisable, !globalReadOnly && !sw.autoBusy)
	enableCtrl(sw.chkAutoExit, !globalReadOnly && !sw.autoBusy)
	enableCtrl(sw.chkStartup, !globalReadOnly && sw.startupKnown && !sw.startupBusy)
	procInvalidateRect.Call(sw.hwnd, 0, 0)
}

func setChecked(hwnd uintptr, checked bool) {
	var v uintptr = bstUnchecked
	if checked {
		v = bstChecked
	}
	procSendMessageW.Call(hwnd, bmSetCheck, v, 0)
}

func isChecked(hwnd uintptr) bool {
	r, _, _ := procSendMessageW.Call(hwnd, bmGetCheck, 0, 0)
	return r == bstChecked
}

func enableCtrl(hwnd uintptr, enabled bool) {
	var flag uintptr
	if enabled {
		flag = 1
	}
	procEnableWindow.Call(hwnd, flag)
}

func moveCtrl(hwnd uintptr, x, y, w, h int32) {
	if hwnd != 0 {
		procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder|swpNoActivate)
	}
}

func makeRect(x, y, w, h int32) winRect {
	return winRect{left: x, top: y, right: x + w, bottom: y + h}
}

func (sw *settingsWindow) layoutChildren() {
	s := func(v int32) int32 { return scalePx(v, sw.dpi) }
	margin := s(16)
	cardW := s(468) - margin*2
	pad := s(14)
	innerX := margin + pad
	innerW := cardW - pad*2

	// Card 1: Device Status
	y := s(14)
	c1Top := y
	y += s(12)
	sw.rcHdrDevice = makeRect(innerX, y, innerW, s(20))
	y += s(22)
	btnW := s(112)
	btnRefreshW := s(88)
	textLeftW := innerW - btnW - btnRefreshW - s(16)
	sw.rcStatusTitle = makeRect(innerX, y, textLeftW, s(24))
	moveCtrl(sw.btnToggle, innerX+innerW-btnW-btnRefreshW-s(8), y+s(2), btnW, s(30))
	moveCtrl(sw.btnRefresh, innerX+innerW-btnRefreshW, y+s(2), btnRefreshW, s(30))
	y += s(34)
	sw.rcDeviceSub = makeRect(innerX, y, innerW, s(26))
	y += s(26)
	sw.rcMonitorState = makeRect(innerX, y, innerW, s(18))
	y += s(24)
	sw.cardRects[0] = winRect{left: margin, top: c1Top, right: margin + cardW, bottom: y}

	// Card 2: Automation
	y += s(12)
	c2Top := y
	y += s(12)
	sw.rcHdrAuto = makeRect(innerX, y, innerW, s(20))
	y += s(24)

	chkSize := s(18)
	lblX := innerX + chkSize + s(8)
	lblW := innerW - chkSize - s(8)

	moveCtrl(sw.chkAutoDisable, innerX, y+s(1), chkSize, chkSize)
	sw.rcLblAutoDisable = makeRect(lblX, y, lblW, s(19))
	y += s(20)
	sw.rcHintAutoDisable = makeRect(lblX, y, lblW, s(28))
	y += s(32)

	moveCtrl(sw.chkAutoExit, innerX, y+s(1), chkSize, chkSize)
	sw.rcLblAutoExit = makeRect(lblX, y, lblW, s(19))
	y += s(20)
	sw.rcHintAutoExit = makeRect(lblX, y, lblW, s(28))
	y += s(32)

	moveCtrl(sw.chkStartup, innerX, y+s(1), chkSize, chkSize)
	sw.rcLblStartup = makeRect(lblX, y, lblW, s(19))
	y += s(20)
	sw.rcHintStartup = makeRect(lblX, y, lblW, s(28))
	y += s(34)
	sw.cardRects[1] = winRect{left: margin, top: c2Top, right: margin + cardW, bottom: y}

	// Card 3: Appearance & Language
	y += s(12)
	c3Top := y
	y += s(12)
	sw.rcHdrAppearance = makeRect(innerX, y, innerW, s(20))
	y += s(24)
	cmbW := s(164)
	sw.rcLblTheme = makeRect(innerX, y+s(4), innerW-cmbW-s(10), s(20))
	moveCtrl(sw.cmbTheme, innerX+innerW-cmbW, y, cmbW, s(140))
	y += s(34)
	sw.rcLblLanguage = makeRect(innerX, y+s(4), innerW-cmbW-s(10), s(20))
	moveCtrl(sw.cmbLanguage, innerX+innerW-cmbW, y, cmbW, s(140))
	y += s(38)
	sw.cardRects[2] = winRect{left: margin, top: c3Top, right: margin + cardW, bottom: y}

	// Footer
	y += s(12)
	closeW := s(86)
	sw.rcFooter = makeRect(margin, y+s(2), cardW-closeW-s(10), s(34))
	moveCtrl(sw.btnClose, margin+cardW-closeW, y, closeW, s(28))
}

func (sw *settingsWindow) refreshStateAsync(includeStartup bool) {
	hwnd := sw.hwnd
	go func() {
		st := readState(false)
		var sKnown, sOn bool
		if includeStartup {
			if on, err := readStartupEnabled(); err == nil {
				sKnown = true
				sOn = on
			}
		}
		sw.tray.postUIUpdate(func() {
			sw.tray.applyFreshState(st)
			if includeStartup && sKnown {
				sw.tray.startupKnown = true
				sw.tray.startupOn = sOn
			}
			if sw.hwnd == hwnd && includeStartup {
				sw.startupKnown = sKnown
				sw.startupOn = sOn
				sw.updatingUI = true
				setChecked(sw.chkStartup, sOn)
				sw.updatingUI = false
				sw.updateLiveStateDisplay()
			}
		})
	}()
}

func (sw *settingsWindow) onToggleAutomation() {
	if sw.updatingUI || sw.autoBusy || globalReadOnly {
		return
	}
	oldAutoDisable := sw.tray.settings.AutoDisable
	oldAutoExit := sw.tray.settings.AutoExit
	oldEnabledAfter := sw.tray.enabledAfter
	baseCfg := sw.tray.settings

	wantDisable := isChecked(sw.chkAutoDisable)
	wantExit := isChecked(sw.chkAutoExit)
	if !oldAutoExit && wantExit {
		sw.tray.enabledAfter = timeMillisNow()
	}

	sw.autoBusy = true
	sw.tray.settings.AutoDisable = wantDisable
	sw.tray.settings.AutoExit = wantExit
	sw.updateLiveStateDisplay()

	hwnd := sw.hwnd
	go func() {
		saveAutomation := func(autoDisable, autoExit bool) error {
			cfg, loadErr := loadUserSettings()
			if loadErr != nil {
				cfg = baseCfg
			}
			cfg.AutoDisable = autoDisable
			cfg.AutoExit = autoExit
			return saveUserSettings(cfg)
		}
		err := saveAutomation(wantDisable, wantExit)
		if err == nil {
			if wantDisable || wantExit {
				err = ensureMonitorRunning(hwnd, sw.tray.requestAutoExitShutdown)
			} else {
				err = stopUUWatch()
			}
			if err != nil {
				_ = saveAutomation(oldAutoDisable, oldAutoExit)
			}
		}
		latestState := readState(false)
		sw.tray.postUIUpdate(func() {
			sw.autoBusy = false
			if err != nil {
				sw.tray.settings.AutoDisable = oldAutoDisable
				sw.tray.settings.AutoExit = oldAutoExit
				sw.tray.enabledAfter = oldEnabledAfter
				if sw.tray.settingsWin == sw && sw.hwnd == hwnd {
					sw.updatingUI = true
					setChecked(sw.chkAutoDisable, oldAutoDisable)
					setChecked(sw.chkAutoExit, oldAutoExit)
					sw.updatingUI = false
				}
				sw.tray.reportError("automation-option", err)
			}
			sw.tray.applyFreshState(latestState)
		})
	}()
}

func (sw *settingsWindow) onToggleStartup() {
	if sw.updatingUI || sw.startupBusy || !sw.startupKnown || globalReadOnly {
		return
	}
	target := isChecked(sw.chkStartup)
	sw.startupBusy = true
	sw.updateLiveStateDisplay()

	hwnd := sw.hwnd
	go func() {
		err := changeStartup(target, hwnd)
		sw.tray.postUIUpdate(func() {
			sw.startupBusy = false
			if err != nil {
				if sw.hwnd == hwnd {
					sw.updatingUI = true
					setChecked(sw.chkStartup, sw.startupOn)
					sw.updatingUI = false
				}
				s := getStrings(sw.tray.settings.Language)
				sw.tray.reportError("startup-toggle", fmt.Errorf("%s%w", s.startupError, err))
			} else {
				sw.startupOn = target
				sw.tray.startupKnown = true
				sw.tray.startupOn = target
				if sw.hwnd == hwnd {
					sw.updatingUI = true
					setChecked(sw.chkStartup, target)
					sw.updatingUI = false
				}
			}
			if sw.hwnd == hwnd {
				sw.updateLiveStateDisplay()
			}
		})
	}()
}

func (sw *settingsWindow) onThemeChanged() {
	if sw.updatingUI {
		return
	}
	sel, _, _ := procSendMessageW.Call(sw.cmbTheme, cbGetCurSel, 0, 0)
	themes := []string{"system", "light", "dark"}
	if int(sel) < 0 || int(sel) >= len(themes) {
		return
	}
	next := themes[sel]
	if next == sw.tray.settings.Theme {
		return
	}
	old := sw.tray.settings.Theme
	sw.tray.settings.Theme = next
	if !globalReadOnly {
		if err := saveUserSettings(sw.tray.settings); err != nil {
			sw.tray.settings.Theme = old
			sw.populateTextsAndValues()
			sw.tray.reportError("save-theme", err)
			return
		}
	}
	sw.rebuildThemeAndFonts()
	sw.populateTextsAndValues()
}

func (sw *settingsWindow) onLanguageChanged() {
	if sw.updatingUI {
		return
	}
	sel, _, _ := procSendMessageW.Call(sw.cmbLanguage, cbGetCurSel, 0, 0)
	langs := []string{"zh-CN", "zh-TW", "en"}
	if int(sel) < 0 || int(sel) >= len(langs) {
		return
	}
	next := langs[sel]
	if next == sw.tray.settings.Language {
		return
	}
	old := sw.tray.settings.Language
	sw.tray.settings.Language = next
	if !globalReadOnly {
		if err := saveUserSettings(sw.tray.settings); err != nil {
			sw.tray.settings.Language = old
			sw.populateTextsAndValues()
			sw.tray.reportError("save-language", err)
			return
		}
	}
	sw.rebuildThemeAndFonts()
	sw.populateTextsAndValues()
	_ = sw.tray.refreshIconAndTooltip()
}

func drawPaintedText(hdc, font uintptr, col uint32, text string, rc winRect, flags uint32) {
	if text == "" {
		return
	}
	var oldFont uintptr
	if font != 0 {
		oldFont, _, _ = procSelectObject.Call(hdc, font)
	}
	procSetTextColor.Call(hdc, uintptr(col))
	ptr, _ := windows.UTF16PtrFromString(text)
	negOne := ^uintptr(0)
	r := rc
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(ptr)), negOne, uintptr(unsafe.Pointer(&r)), uintptr(flags))
	runtime.KeepAlive(ptr)
	if oldFont != 0 {
		procSelectObject.Call(hdc, oldFont)
	}
}

func (sw *settingsWindow) paintBackgroundAndCards(hdc uintptr) {
	var rc winRect
	procGetClientRect.Call(sw.hwnd, uintptr(unsafe.Pointer(&rc)))
	if sw.bgBrush != 0 {
		procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), sw.bgBrush)
	}
	if sw.cardBrush == 0 {
		return
	}
	var borderCol, fgPrimary, fgSecondary uint32
	if sw.hc {
		borderCol = sysColor(colorWindowText)
		fgPrimary = sysColor(colorWindowText)
		fgSecondary = sysColor(colorGrayText)
	} else if sw.dark {
		borderCol = colorRef(63, 63, 70)
		fgPrimary = colorRef(244, 244, 245)
		fgSecondary = colorRef(161, 161, 170)
	} else {
		borderCol = colorRef(229, 231, 235)
		fgPrimary = colorRef(17, 24, 39)
		fgSecondary = colorRef(107, 114, 128)
	}
	pen, _, _ := procCreatePen.Call(0, uintptr(scalePx(1, sw.dpi)), uintptr(borderCol))
	if pen != 0 {
		oldPen, _, _ := procSelectObject.Call(hdc, pen)
		oldBrush, _, _ := procSelectObject.Call(hdc, sw.cardBrush)
		radius := uintptr(scalePx(10, sw.dpi))
		for _, card := range sw.cardRects {
			procRoundRect.Call(hdc, uintptr(card.left), uintptr(card.top), uintptr(card.right), uintptr(card.bottom), radius, radius)
		}
		if oldBrush != 0 {
			procSelectObject.Call(hdc, oldBrush)
		}
		if oldPen != 0 {
			procSelectObject.Call(hdc, oldPen)
		}
		procDeleteObject.Call(pen)
	}

	s := getStrings(sw.tray.settings.Language)
	st := sw.tray.cachedState
	busy := sw.tray.isToggling()

	var statusLabel string
	subLabel := fmt.Sprintf("%s (%s)", deviceName, deviceID)
	if busy {
		statusLabel = s.deviceSwitching
	} else if st.Status == "enabled" {
		statusLabel = s.deviceEnabled
	} else if st.Status == "disabled" {
		statusLabel = s.deviceDisabled
	} else {
		statusLabel = s.deviceUnavailable
		if st.Message != "" {
			subLabel = st.Message
		}
	}

	procSetBkMode.Call(hdc, 1) // TRANSPARENT
	const singleLine = dtLeft | dtSingleLine | dtEndEllipsis | dtNoPrefix
	const chkLabelFlags = dtLeft | dtSingleLine | dtVCenter | dtEndEllipsis | dtNoPrefix
	const wrapFlags = dtLeft | dtWordBreak | dtNoPrefix

	drawPaintedText(hdc, sw.fontBold, fgPrimary, s.sectionDevice, sw.rcHdrDevice, singleLine)
	drawPaintedText(hdc, sw.fontBold, fgPrimary, s.sectionAutomation, sw.rcHdrAuto, singleLine)
	drawPaintedText(hdc, sw.fontBold, fgPrimary, s.sectionAppearance, sw.rcHdrAppearance, singleLine)
	drawPaintedText(hdc, sw.fontHeading, fgPrimary, statusLabel, sw.rcStatusTitle, singleLine)

	drawPaintedText(hdc, sw.fontBody, fgPrimary, s.optAutoDisable, sw.rcLblAutoDisable, chkLabelFlags)
	drawPaintedText(hdc, sw.fontBody, fgPrimary, s.optAutoExit, sw.rcLblAutoExit, chkLabelFlags)
	drawPaintedText(hdc, sw.fontBody, fgPrimary, s.optStartup, sw.rcLblStartup, chkLabelFlags)
	drawPaintedText(hdc, sw.fontBody, fgPrimary, s.lblTheme, sw.rcLblTheme, singleLine)
	drawPaintedText(hdc, sw.fontBody, fgPrimary, s.lblLanguage, sw.rcLblLanguage, singleLine)

	drawPaintedText(hdc, sw.fontHint, fgSecondary, subLabel, sw.rcDeviceSub, wrapFlags)
	drawPaintedText(hdc, sw.fontHint, fgSecondary, s.monitorPrefix+monitorStatusText(s, st), sw.rcMonitorState, singleLine)
	drawPaintedText(hdc, sw.fontHint, fgSecondary, s.optAutoDisableHint, sw.rcHintAutoDisable, wrapFlags)
	drawPaintedText(hdc, sw.fontHint, fgSecondary, s.optAutoExitHint, sw.rcHintAutoExit, wrapFlags)
	drawPaintedText(hdc, sw.fontHint, fgSecondary, s.optStartupHint, sw.rcHintStartup, wrapFlags)
	drawPaintedText(hdc, sw.fontHint, fgSecondary, s.footerAbout, sw.rcFooter, wrapFlags)
}

func settingsWindowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	tray := currentResidentTray
	var sw *settingsWindow
	if tray != nil && tray.settingsWin != nil && tray.settingsWin.hwnd == hwnd {
		sw = tray.settingsWin
	}
	if sw == nil {
		ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
		return ret
	}

	switch message {
	case wmEraseBkgnd:
		return 1

	case wmPaint:
		var ps paintStruct
		hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc != 0 {
			sw.paintBackgroundAndCards(hdc)
			procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0

	case wmLButtonUp:
		x := int32(int16(lParam & 0xFFFF))
		y := int32(int16((lParam >> 16) & 0xFFFF))
		if ptInRect(sw.rcLblAutoDisable, x, y) && !globalReadOnly && !sw.autoBusy {
			setChecked(sw.chkAutoDisable, !isChecked(sw.chkAutoDisable))
			sw.onToggleAutomation()
		} else if ptInRect(sw.rcLblAutoExit, x, y) && !globalReadOnly && !sw.autoBusy {
			setChecked(sw.chkAutoExit, !isChecked(sw.chkAutoExit))
			sw.onToggleAutomation()
		} else if ptInRect(sw.rcLblStartup, x, y) && !globalReadOnly && sw.startupKnown && !sw.startupBusy {
			setChecked(sw.chkStartup, !isChecked(sw.chkStartup))
			sw.onToggleStartup()
		}
		return 0

	case wmCtlColorStatic, wmCtlColorBtn, wmCtlColorListBox:
		hdc := wParam
		ctrl := lParam
		isFooter := ctrl == sw.btnClose
		var bgCol, fgCol uint32
		if sw.hc {
			bgCol = sysColor(colorWindow)
			fgCol = sysColor(colorWindowText)
			procSetBkMode.Call(hdc, 2) // OPAQUE
			procSetBkColor.Call(hdc, uintptr(bgCol))
			procSetTextColor.Call(hdc, uintptr(fgCol))
			return sw.bgBrush
		}
		if sw.dark {
			if message == wmCtlColorListBox || !isFooter {
				bgCol = colorRef(39, 39, 42)
			} else {
				bgCol = colorRef(24, 24, 27)
			}
			fgCol = colorRef(244, 244, 245)
		} else {
			if message == wmCtlColorListBox || !isFooter {
				bgCol = colorRef(255, 255, 255)
			} else {
				bgCol = colorRef(243, 244, 246)
			}
			fgCol = colorRef(17, 24, 39)
		}
		procSetBkMode.Call(hdc, 2) // OPAQUE
		procSetBkColor.Call(hdc, uintptr(bgCol))
		procSetTextColor.Call(hdc, uintptr(fgCol))
		if message == wmCtlColorListBox || !isFooter {
			return sw.cardBrush
		}
		return sw.bgBrush

	case wmCommand:
		id := uint16(wParam & 0xFFFF)
		code := uint16((wParam >> 16) & 0xFFFF)
		switch id {
		case idBtnToggle:
			if code == bnClicked {
				sw.tray.toggleDeviceAsync(hwnd)
			}
			return 0
		case idBtnRefresh:
			if code == bnClicked {
				sw.refreshStateAsync(true)
			}
			return 0
		case idChkAutoDisable:
			if code == bnClicked {
				sw.onToggleAutomation()
			}
			return 0
		case idChkAutoExit:
			if code == bnClicked {
				sw.onToggleAutomation()
			}
			return 0
		case idChkStartup:
			if code == bnClicked {
				sw.onToggleStartup()
			}
			return 0
		case idCmbTheme:
			if code == cbnSelChange {
				sw.onThemeChanged()
			}
			return 0
		case idCmbLanguage:
			if code == cbnSelChange {
				sw.onLanguageChanged()
			}
			return 0
		case idBtnClose:
			if code == bnClicked {
				procDestroyWindow.Call(hwnd)
			}
			return 0
		}

	case wmDpiChanged:
		newDPI := uint32(wParam & 0xFFFF)
		if newDPI > 0 && newDPI != sw.dpi {
			sw.dpi = newDPI
			if lParam != 0 {
				var suggested winRect
				copyFromWin32(unsafe.Pointer(&suggested), lParam, unsafe.Sizeof(suggested))
				procSetWindowPos.Call(
					hwnd, 0,
					uintptr(suggested.left), uintptr(suggested.top),
					uintptr(suggested.right-suggested.left), uintptr(suggested.bottom-suggested.top),
					swpNoZOrder|swpNoActivate,
				)
			}
			sw.destroyIcons()
			sw.smallIcon = loadEmbeddedIconAtSize(smallIconSizeForDPI(sw.dpi))
			sw.bigIcon = loadEmbeddedIconAtSize(largeIconSizeForDPI(sw.dpi))
			if sw.smallIcon != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, 0, sw.smallIcon)
			}
			if sw.bigIcon != 0 {
				procSendMessageW.Call(hwnd, wmSetIcon, 1, sw.bigIcon)
			}
			sw.rebuildThemeAndFonts()
			sw.layoutChildren()
			procInvalidateRect.Call(hwnd, 0, 0)
		}
		return 0

	case wmSettingChange:
		nowHC := isHighContrastOn()
		nowDark := !nowHC && effectiveDarkTheme(sw.tray.settings.Theme)
		if nowHC != sw.hc || nowDark != sw.dark {
			sw.rebuildThemeAndFonts()
		}
		return 0

	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0

	case wmNcDestroy:
		sw.freeGDIResources()
		sw.destroyIcons()
		sw.hwnd = 0
		if tray.settingsWin == sw {
			tray.settingsWin = nil
		}
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}
