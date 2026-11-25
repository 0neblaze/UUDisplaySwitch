package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestTrayCallbackActionMinimalistContract(t *testing.T) {
	pack := func(event uint32, iconID uint16) uint32 {
		return uint32(uint16(event)) | uint32(iconID)<<16
	}
	tests := []struct {
		name     string
		callback uint32
		want     string
		ok       bool
	}{
		{"left single click toggles adapter", pack(ninSelect, trayIconID), "toggle", true},
		{"keyboard selection toggles adapter", pack(ninKeySelect, trayIconID), "toggle", true},
		{"double click ignored to prevent double-toggle", pack(wmLButtonDblClk, trayIconID), "", false},
		{"right button up opens menu", pack(wmRButtonUp, trayIconID), "menu", true},
		{"context menu opens menu", pack(wmContextMenu, trayIconID), "menu", true},
		{"other icon ignored", pack(ninSelect, trayIconID+1), "", false},
	}
	for _, test := range tests {
		got, ok := trayCallbackAction(test.callback, trayIconID)
		if got != test.want || ok != test.ok {
			t.Errorf("%s: trayCallbackAction() = (%q, %t), want (%q, %t)",
				test.name, got, ok, test.want, test.ok)
		}
	}
}

func TestRemoteDisableConfirmationPolicy(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		uu      uuSnapshot
		scanErr error
		want    bool
	}{
		{"disable while GameViewer running prompts confirmation", "disable", uuSnapshot{Running: true}, nil, true},
		{"disable while GameViewer absent switches immediately", "disable", uuSnapshot{Running: false}, nil, false},
		{"disable while only updating switches immediately", "disable", uuSnapshot{Updating: true}, nil, false},
		{"disable with scan error does not block on remote prompt", "disable", uuSnapshot{Running: true}, errors.New("scan failed"), false},
		{"enable while GameViewer running never prompts", "enable", uuSnapshot{Running: true}, nil, false},
	}
	for _, tc := range tests {
		if got := shouldConfirmRemoteDisable(tc.mode, tc.uu, tc.scanErr); got != tc.want {
			t.Errorf("%s: shouldConfirmRemoteDisable() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNextToggleMode(t *testing.T) {
	if mode, err := nextToggleMode(0); err != nil || mode != "disable" {
		t.Fatalf("problem 0 -> (%q, %v), want (\"disable\", nil)", mode, err)
	}
	if mode, err := nextToggleMode(22); err != nil || mode != "enable" {
		t.Fatalf("problem 22 -> (%q, %v), want (\"enable\", nil)", mode, err)
	}
	if _, err := nextToggleMode(43); err == nil {
		t.Fatal("problem 43 should reject toggle")
	}
}

func TestLocalizationStringsSupportedLanguages(t *testing.T) {
	for _, tc := range []struct {
		lang     string
		wantApp  string
		wantMenu string
		wantExit string
	}{
		{"zh-CN", "UU 虚拟屏", "设置…", "退出"},
		{"zh-TW", "UU 虛擬螢幕", "設定…", "退出"},
		{"en", "UU Display", "Settings…", "Exit"},
		{"invalid", "UU 虚拟屏", "设置…", "退出"},
	} {
		got := getStrings(tc.lang)
		if got.app != tc.wantApp || got.menuSettings != tc.wantMenu || got.menuExit != tc.wantExit {
			t.Errorf("getStrings(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.lang, got.app, got.menuSettings, got.menuExit, tc.wantApp, tc.wantMenu, tc.wantExit)
		}
		for _, forbidden := range []string{"无畏契约", "特戰英豪", "VALORANT"} {
			if strings.Contains(got.gameRestartBody, forbidden) || strings.Contains(got.gameRestartTitle, forbidden) {
				t.Errorf("getStrings(%q) gameRestart text contains forbidden game name %q", tc.lang, forbidden)
			}
		}
	}
}

func TestUserSettingsPreservesLegacyBackdropAndValidatesValues(t *testing.T) {
	tmpLocal := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmpLocal)
	dir := filepath.Join(tmpLocal, "UUDisplaySwitch")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := `{"theme":"system","language":"zh-CN","backdrop":"mica-alt","autoDisable":true,"autoExit":true}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadUserSettings()
	if err != nil {
		t.Fatalf("loadUserSettings failed on legacy backdrop: %v", err)
	}
	if !cfg.AutoDisable || !cfg.AutoExit || cfg.Backdrop != "mica-alt" {
		t.Fatalf("unexpected loaded settings: %+v", cfg)
	}
	cfg.Theme = "dark"
	if err := saveUserSettings(cfg); err != nil {
		t.Fatalf("saveUserSettings failed: %v", err)
	}
	reloaded, err := loadUserSettings()
	if err != nil || reloaded.Theme != "dark" || reloaded.Backdrop != "mica-alt" {
		t.Fatalf("roundtrip settings mismatch: %+v, err=%v", reloaded, err)
	}
}

func TestStartupTaskXMLOwnershipAndUTF16HeaderNormalization(t *testing.T) {
	const sid = "S-1-5-21-1111111111-2222222222-3333333333-1001"
	newExe := protectedMonitorPathForSID(sid)
	legacyExe := legacyProtectedMonitorPathForSID(sid)

	xmlNew := strings.Replace(createStartupTaskXML(sid, newExe), `encoding="UTF-8"`, `encoding="UTF-16"`, 1)
	if enabled, err := verifyOwnedStartupTaskXML([]byte(xmlNew), sid); err != nil || !enabled {
		t.Fatalf("UTF-16 header new task XML rejected: enabled=%v err=%v", enabled, err)
	}

	xmlLegacy := createStartupTaskXML(sid, legacyExe)
	if enabled, err := verifyOwnedStartupTaskXML([]byte(xmlLegacy), sid); err != nil || !enabled {
		t.Fatalf("legacy UUDisplayCore.exe task XML rejected: enabled=%v err=%v", enabled, err)
	}

	xmlDisabled := strings.Replace(xmlNew, "<Enabled>true</Enabled>", "<Enabled>false</Enabled>", -1)
	if enabled, err := verifyOwnedStartupTaskXML([]byte(xmlDisabled), sid); err != nil || enabled {
		t.Fatalf("disabled task state mismatch: enabled=%v err=%v", enabled, err)
	}

	// An owned task whose LogonTrigger was cleared (<Triggers />) must remain owned (err == nil)
	// so it can be replaced/deleted, while reporting enabled == false in Settings.
	xmlTriggerless := strings.Replace(xmlNew, "<Triggers>\n    <LogonTrigger>\n      <Enabled>true</Enabled>\n      <UserId>"+sid+"</UserId>\n      <Delay>PT10S</Delay>\n    </LogonTrigger>\n  </Triggers>", "<Triggers />", 1)
	if enabled, err := verifyOwnedStartupTaskXML([]byte(xmlTriggerless), sid); err != nil || enabled {
		t.Fatalf("triggerless owned task state mismatch: enabled=%v err=%v", enabled, err)
	}

	for _, bad := range []string{
		strings.Replace(xmlNew, "--watch", "--apply disable", 1),
		strings.Replace(xmlNew, startupTaskSource, "ForeignSource", 1),
		strings.Replace(xmlNew, "HighestAvailable", "LeastPrivilege", 1),
		strings.Replace(xmlNew, sid, "S-1-5-21-9999-1001", -1),
		strings.Replace(xmlNew, "</Exec>", "</Exec><ComHandler><ClassId>{0000}</ClassId></ComHandler>", 1),
		strings.Replace(xmlNew, "</Task>", "", 1),
	} {
		if _, err := verifyOwnedStartupTaskXML([]byte(bad), sid); err == nil {
			t.Fatalf("tampered or malformed startup XML was unexpectedly accepted: %s", bad)
		}
	}

	escaped := escapeXMLText(`A&B <tag> "q" 's'`)
	if escaped != `A&amp;B &lt;tag&gt; &quot;q&quot; &apos;s&apos;` {
		t.Fatalf("unexpected XML escaping: %q", escaped)
	}
	if unescaped := unescapeXMLText(escaped); unescaped != `A&B <tag> "q" 's'` {
		t.Fatalf("unexpected XML unescaping: %q", unescaped)
	}

	// Numeric decimal/hex entities and inline comments must decode cleanly.
	xmlEntities := strings.Replace(xmlNew, "--watch", "&#45;&#45;watch", 1)
	xmlEntities = strings.Replace(xmlEntities, startupTaskSource, "UUDisplaySwitch&#x2e;Startup.v1", 1)
	if enabled, err := verifyOwnedStartupTaskXML([]byte(xmlEntities), sid); err != nil || !enabled {
		t.Fatalf("numeric entity startup XML rejected: enabled=%v err=%v", enabled, err)
	}

	// Duplicate <Actions>, <Principals>, or <Task> blocks must be rejected.
	dupActions := strings.Replace(xmlNew, "</Actions>", "</Actions><Actions Context=\"Other\"><Exec><Command>cmd.exe</Command></Exec></Actions>", 1)
	if _, err := verifyOwnedStartupTaskXML([]byte(dupActions), sid); err == nil {
		t.Fatal("duplicate <Actions> block was accepted")
	}
	dupPrincipals := strings.Replace(xmlNew, "</Principals>", "</Principals><Principals><Principal id=\"X\"/></Principals>", 1)
	if _, err := verifyOwnedStartupTaskXML([]byte(dupPrincipals), sid); err == nil {
		t.Fatal("duplicate <Principals> block was accepted")
	}
}

func TestTrayShouldAutoExitRequiresFreshConfirmedCompletion(t *testing.T) {
	base := appState{
		Status:        "disabled",
		UUStateKnown:  true,
		WatcherStatus: "stopped",
		UUExitAt:      200,
	}
	autoExit := autoPreferences{AutoExit: true}
	autoDisable := autoPreferences{AutoDisable: true, AutoExit: true}
	tests := []struct {
		name        string
		preferences autoPreferences
		state       appState
		enabledAt   int64
		want        bool
	}{
		{"fresh completion and exit-only", autoExit, base, 100, true},
		{"fresh completion with successful disable", autoDisable, base, 100, true},
		{"exit preference disabled", autoPreferences{}, base, 100, false},
		{"completion predates resident or enable baseline", autoExit, base, 200, false},
		{"UU is running", autoExit, func() appState { s := base; s.UURunning = true; return s }(), 100, false},
		{"UU is updating", autoExit, func() appState { s := base; s.UUUpdating = true; return s }(), 100, false},
		{"UU state is unknown", autoExit, func() appState { s := base; s.UUStateKnown = false; return s }(), 100, false},
		{"monitor is active", autoExit, func() appState { s := base; s.WatcherActive = true; return s }(), 100, false},
		{"monitor has an error", autoExit, func() appState { s := base; s.WatcherStatus = "error"; return s }(), 100, false},
		{"required device disable failed", autoDisable, func() appState { s := base; s.Status = "enabled"; return s }(), 100, false},
	}
	for _, test := range tests {
		got := trayShouldAutoExit(test.preferences, test.state, test.enabledAt)
		if got != test.want {
			t.Errorf("%s: trayShouldAutoExit() = %t, want %t", test.name, got, test.want)
		}
	}
}

func TestTrayPathComparisonIgnoresCaseAndExtendedPrefix(t *testing.T) {
	if !sameTrayPath("C:\\Apps\\UUDisplaySwitch.exe", "c:\\apps\\uudisplayswitch.EXE") {
		t.Fatal("equivalent Windows executable paths did not match")
	}
	if !sameTrayPath("C:\\Apps\\UUDisplaySwitch.exe", "\\\\?\\C:\\Apps\\UUDisplaySwitch.exe") {
		t.Fatal("extended-length path did not match its normal path")
	}
	if sameTrayPath("C:\\Apps\\UUDisplaySwitch.exe", "C:\\Other\\UUDisplaySwitch.exe") {
		t.Fatal("different installation paths matched")
	}
}

func TestDecodeMenuAnchorPreservesSignedCoordinates(t *testing.T) {
	packed := uintptr(uint32(uint16(0xff38)) | uint32(uint16(0x0384))<<16)
	x, y := decodeMenuAnchor(packed)
	if x != -200 || y != 900 || menuAnchorNeedsCursorPos(x, y) {
		t.Fatalf("decodeMenuAnchor() = (%d, %d), want (-200, 900) without cursor fallback", x, y)
	}
	kbX, kbY := decodeMenuAnchor(uintptr(0xffffffff))
	if kbX != -1 || kbY != -1 || !menuAnchorNeedsCursorPos(kbX, kbY) {
		t.Fatalf("keyboard anchor (-1, -1) did not trigger cursor fallback: (%d, %d)", kbX, kbY)
	}
	if !menuAnchorNeedsCursorPos(-32768, -32768) {
		t.Fatal("sentinel (-32768, -32768) did not trigger cursor fallback")
	}
}

func TestHandleMonitorStateEventCoalescesPendingSignal(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondDone := make(chan struct{})
	calls := 0

	tray := &residentTray{
		settings:     defaultUserSettings(),
		enabledAfter: 100,
		stateForMonitorEvent: func() (appState, userSettings, error) {
			calls++
			if calls == 1 {
				close(firstStarted)
				<-releaseFirst
				return appState{Status: "disabled", WatcherStatus: "grace"}, defaultUserSettings(), nil
			}
			close(secondDone)
			return appState{Status: "disabled", WatcherStatus: "stopped", UUExitAt: 200}, defaultUserSettings(), nil
		},
	}

	tray.handleMonitorStateEvent()
	<-firstStarted

	// Second signal arrives while first readState is still in flight -> must coalesce, not drop.
	tray.handleMonitorStateEvent()
	if !tray.monitorEventPending {
		t.Fatal("expected monitorEventPending to be set while checkingAutoExit is true")
	}

	close(releaseFirst)
	// Wait until the first goroutine posts its UI update.
	deadline := time.Now().Add(2 * time.Second)
	for {
		tray.drainUIUpdates()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("coalesced second monitor event did not start after first completed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	<-secondDone
	deadline = time.Now().Add(2 * time.Second)
	for tray.checkingAutoExit {
		tray.drainUIUpdates()
		if time.Now().After(deadline) {
			t.Fatal("second monitor event did not finish draining")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if calls != 2 || tray.cachedState.UUExitAt != 200 || tray.monitorEventPending {
		t.Fatalf("unexpected coalescing result: calls=%d cached=%+v pending=%v", calls, tray.cachedState, tray.monitorEventPending)
	}
}

func TestReadStateRespectsIncludeGameFlagAndSingleSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmpDir)
	stNoGame := readState(false)
	if stNoGame.GameRunning {
		t.Fatal("readState(false) should skip game process snapshot and keep GameRunning=false")
	}
	// Write a completed stopped heartbeat and verify readState(true) derives UUExitAt from the single read.
	id := heartbeat{
		PID:      uint32(os.Getpid()),
		At:       time.Now().Unix(),
		Started:  123456,
		Token:    "0123456789abcdef0123456789abcdef",
		Status:   "stopped",
		UUExitAt: 1781548800123,
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "UUDisplaySwitch"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomically(filepath.Join(tmpDir, "UUDisplaySwitch", "monitor.json"), id); err != nil {
		t.Fatal(err)
	}
	stWithGame := readState(true)
	if stWithGame.Device != deviceName || stWithGame.WatcherActive || stWithGame.WatcherStatus != "stopped" {
		t.Fatalf("unexpected readState(true) fields: %+v", stWithGame)
	}
	if !stWithGame.UURunning && !stWithGame.UUUpdating && stWithGame.UUStateKnown && stWithGame.UUExitAt != 1781548800123 {
		t.Fatalf("readState(true) did not derive UUExitAt from monitor.json: got %d, want %d", stWithGame.UUExitAt, id.UUExitAt)
	}
}

func TestPackPoint64PreservesSignedCoordinates(t *testing.T) {
	expected := func(x, y int32) uint64 {
		return uint64(uint32(x)) | (uint64(uint32(y)) << 32)
	}
	cases := []struct {
		x, y int32
		want uint64
	}{
		{0, 0, 0},
		{100, 200, (uint64(200) << 32) | 100},
		{-1920, -1080, expected(-1920, -1080)},
		{100, -50, expected(100, -50)},
	}
	for _, tc := range cases {
		got := uint64(packPoint64(tc.x, tc.y))
		if got != tc.want {
			t.Errorf("packPoint64(%d, %d) = 0x%016x, want 0x%016x", tc.x, tc.y, got, tc.want)
		}
		gotX := int32(uint32(got & 0xffffffff))
		gotY := int32(uint32(got >> 32))
		if gotX != tc.x || gotY != tc.y {
			t.Errorf("unpacked packPoint64(%d, %d) = (%d, %d)", tc.x, tc.y, gotX, gotY)
		}
	}
}

func TestHandleMonitorStateEventPreservesUIThemeLanguageAndInFlightAutoEdit(t *testing.T) {
	tray := &residentTray{
		settings: userSettings{
			Theme:       "dark",
			Language:    "en",
			AutoDisable: true,
			AutoExit:    false,
		},
		stateForMonitorEvent: func() (appState, userSettings, error) {
			return appState{Status: "disabled", WatcherStatus: "waiting", WatcherActive: true},
				userSettings{Theme: "light", Language: "zh-CN", AutoDisable: false, AutoExit: true},
				nil
		},
	}
	// Simulate an active Settings window with an in-flight automation toggle.
	tray.settingsWin = &settingsWindow{tray: tray, autoBusy: true}

	tray.handleMonitorStateEvent()
	deadline := time.Now().Add(2 * time.Second)
	for tray.checkingAutoExit {
		tray.drainUIUpdates()
		if time.Now().After(deadline) {
			t.Fatal("handleMonitorStateEvent did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if tray.settings.Theme != "dark" || tray.settings.Language != "en" {
		t.Fatalf("handleMonitorStateEvent overwrote UI Theme/Language: %+v", tray.settings)
	}
	if !tray.settings.AutoDisable || tray.settings.AutoExit {
		t.Fatalf("handleMonitorStateEvent overwrote in-flight autoBusy preferences: %+v", tray.settings)
	}

	// Once autoBusy clears, AutoDisable/AutoExit update while Theme/Language still stay UI-owned.
	tray.settingsWin.autoBusy = false
	tray.handleMonitorStateEvent()
	deadline = time.Now().Add(2 * time.Second)
	for tray.checkingAutoExit {
		tray.drainUIUpdates()
		if time.Now().After(deadline) {
			t.Fatal("second handleMonitorStateEvent did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if tray.settings.Theme != "dark" || tray.settings.Language != "en" {
		t.Fatalf("second handleMonitorStateEvent overwrote UI Theme/Language: %+v", tray.settings)
	}
	if tray.settings.AutoDisable || !tray.settings.AutoExit {
		t.Fatalf("second handleMonitorStateEvent did not sync AutoDisable/AutoExit: %+v", tray.settings)
	}
}

func TestApplyFreshStatePreservesToggleStatusWhileToggling(t *testing.T) {
	tray := &residentTray{
		cachedState: appState{Status: "enabled", Problem: 0, Message: ""},
	}
	tray.toggling.Store(true)
	tray.applyFreshState(appState{Status: "disabled", Problem: 22, Message: "stale", WatcherActive: true, WatcherStatus: "armed"})
	if tray.cachedState.Status != "enabled" || tray.cachedState.Problem != 0 || tray.cachedState.Message != "" {
		t.Fatalf("applyFreshState overwrote toggling status: %+v", tray.cachedState)
	}
	if !tray.cachedState.WatcherActive || tray.cachedState.WatcherStatus != "armed" {
		t.Fatalf("applyFreshState did not update watcher fields: %+v", tray.cachedState)
	}
}

func TestEmbeddedIconSetCreatesAndDestroysCleanly(t *testing.T) {
	// Warm up Win32 per-process GDI stock table (0 -> 4 on first GDI call in a console test process).
	warmup := newTrayIconSet(144)
	warmup.Destroy()

	gdiBefore, _ := currentProcessGuiCounts()
	for i := 0; i < 10; i++ {
		set := newTrayIconSet(144)
		if set.baseSmall == 0 || set.enabledIcon == 0 || set.disabledIcon == 0 || set.busyIcon == 0 || set.errorIcon == 0 {
			set.Destroy()
			t.Fatalf("icon set failed to create badged icons at 144 DPI: %+v", set)
		}
		set.Destroy()
	}
	gdiAfter, _ := currentProcessGuiCounts()
	if gdiAfter != gdiBefore {
		t.Fatalf("GDI handles leaked after 10 icon set cycles: %d -> %d", gdiBefore, gdiAfter)
	}
}

func TestTrayCleanupLeavesNoStaleWMQuitOnSequentialLoops(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t.Setenv("UUDISPLAYSWITCH_TEST_NS", "seq_wmquit")
	prevTray := currentResidentTray
	defer func() { currentResidentTray = prevTray }()

	for iter := 1; iter <= 2; iter++ {
		wakeEvt, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			t.Fatalf("iter %d CreateEvent failed: %v", iter, err)
		}
		tray := &residentTray{
			uiWakeEvt:  wakeEvt,
			settings:   defaultUserSettings(),
			shutdownCh: make(chan struct{}),
		}
		currentResidentTray = tray
		if err := tray.createWindow(); err != nil {
			tray.cleanup()
			t.Fatalf("iter %d createWindow failed: %v", iter, err)
		}

		ran := false
		go func(tr *residentTray) {
			time.Sleep(15 * time.Millisecond)
			tr.postUIUpdate(func() {
				ran = true
				_ = tr.shutdown()
			})
		}(tray)

		if err := tray.messageLoop(); err != nil {
			tray.cleanup()
			t.Fatalf("iter %d messageLoop failed: %v", iter, err)
		}
		if !ran {
			tray.cleanup()
			t.Fatalf("iter %d messageLoop exited prematurely before posted shutdown callback ran (stale WM_QUIT)", iter)
		}
		tray.cleanup()

		var msg trayMessage
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, wmQuit, wmQuit, pmRemove)
		runtime.KeepAlive(&msg)
		if r != 0 {
			t.Fatalf("iter %d left stale WM_QUIT on OS thread queue after cleanup()", iter)
		}
	}
}

func TestTrayWakeUIAndCleanupConcurrentSafety(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		wakeEvt, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			t.Fatalf("iter %d CreateEvent failed: %v", iter, err)
		}
		tray := &residentTray{
			uiWakeEvt:  wakeEvt,
			shutdownCh: make(chan struct{}),
		}
		// Verify normal wakeUI signals the event before cleanup.
		tray.wakeUI()
		res, waitErr := windows.WaitForSingleObject(wakeEvt, 0)
		if waitErr != nil || res != windows.WAIT_OBJECT_0 {
			tray.cleanup()
			t.Fatalf("iter %d wakeUI did not signal event: res=%d err=%v", iter, res, waitErr)
		}

		var wg sync.WaitGroup
		stop := make(chan struct{})
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						tray.wakeUI()
						tray.postUIUpdate(func() {})
					}
				}
			}()
		}
		time.Sleep(time.Millisecond)
		tray.cleanup()
		close(stop)
		wg.Wait()

		if tray.uiWakeEvt != 0 || !tray.shuttingDown || tray.uiQueue != nil {
			t.Fatalf("iter %d post-cleanup state invalid: wakeEvt=%v shuttingDown=%v queueLen=%d",
				iter, tray.uiWakeEvt, tray.shuttingDown, len(tray.uiQueue))
		}
		// Post-cleanup calls must be clean no-ops.
		tray.wakeUI()
		tray.postUIUpdate(func() {})
		if len(tray.uiQueue) != 0 {
			t.Fatalf("iter %d postUIUpdate queued callback after cleanup", iter)
		}
	}
}
