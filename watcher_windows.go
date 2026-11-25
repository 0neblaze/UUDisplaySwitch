package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type uuSnapshot struct {
	Running  bool
	Updating bool
}

type exitDetector struct {
	Seen        bool
	Handled     bool
	AbsentSince time.Time
}

// A minimized/tray window still has GameViewer.exe. One action per confirmed exit.
func (d *exitDetector) Observe(s uuSnapshot, now time.Time) bool {
	if s.Running {
		d.Seen = true
		d.Handled = false
		d.AbsentSince = time.Time{}
		return false
	}
	if s.Updating {
		d.AbsentSince = time.Time{}
		return false
	}
	if !d.Seen || d.Handled {
		return false
	}
	if d.AbsentSince.IsZero() {
		d.AbsentSince = now
		return false
	}
	if now.Sub(d.AbsentSince) < 6*time.Second {
		return false
	}
	d.Handled = true
	return true
}

func queryHandleImagePath(h windows.Handle) (string, error) {
	var stackBuf [1024]uint16
	size := uint32(len(stackBuf))
	if err := windows.QueryFullProcessImageName(h, 0, &stackBuf[0], &size); err == nil {
		return windows.UTF16ToString(stackBuf[:size]), nil
	}
	heapBuf := make([]uint16, 32768)
	size = uint32(len(heapBuf))
	if err := windows.QueryFullProcessImageName(h, 0, &heapBuf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(heapBuf[:size]), nil
}

type processCandidate struct {
	PID  uint32
	Name string
}

func isTransientProcessErr(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

func evaluateUUCandidates(
	root string,
	entries []processCandidate,
	openRunningHandles bool,
	inspect func(pid uint32, needSync bool) (string, windows.Handle, error),
) (uuSnapshot, []windows.Handle, error) {
	var result uuSnapshot
	var handles []windows.Handle
	closeAll := func() {
		for _, h := range handles {
			_ = windows.CloseHandle(h)
		}
		handles = nil
	}

	for _, entry := range entries {
		path, h, err := inspect(entry.PID, openRunningHandles)
		if err != nil {
			if isTransientProcessErr(err) {
				continue
			}
			closeAll()
			return result, nil, err
		}
		if strings.HasPrefix(path, root) {
			if entry.Name == "gameviewer.exe" {
				result.Running = true
			} else {
				result.Updating = true
			}
			if openRunningHandles && h != 0 && len(handles) < 60 {
				handles = append(handles, h)
				h = 0
			}
		}
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}
	return result, handles, nil
}

func scanProcesses(openRunningHandles, checkGame bool) (uuSnapshot, []windows.Handle, bool, error) {
	snap, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return uuSnapshot{}, nil, false, e
	}
	defer windows.CloseHandle(snap)

	var candidates []processCandidate
	gameActive := false
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	e = windows.Process32First(snap, &entry)
	for e == nil {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		switch name {
		case "gameviewer.exe", "upgrade.exe", "gameviewerlauncher.exe":
			candidates = append(candidates, processCandidate{PID: entry.ProcessID, Name: name})
		case "valorant-win64-shipping.exe":
			if checkGame {
				gameActive = true
			}
		}
		e = windows.Process32Next(snap, &entry)
	}
	if !errors.Is(e, windows.ERROR_NO_MORE_FILES) {
		return uuSnapshot{}, nil, false, e
	}
	if len(candidates) == 0 {
		return uuSnapshot{}, nil, gameActive, nil
	}

	root := strings.ToLower(filepath.Join(os.Getenv("ProgramFiles"), "Netease", "GameViewer")) + `\`
	uuSnap, handles, err := evaluateUUCandidates(root, candidates, openRunningHandles, func(pid uint32, needSync bool) (string, windows.Handle, error) {
		access := uint32(windows.PROCESS_QUERY_LIMITED_INFORMATION)
		if needSync {
			access |= windows.SYNCHRONIZE
		}
		h, err := windows.OpenProcess(access, false, pid)
		if err != nil && needSync {
			// Fall back to query-only if SYNCHRONIZE was transiently denied.
			h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
			access = windows.PROCESS_QUERY_LIMITED_INFORMATION
		}
		if err != nil {
			return "", 0, fmt.Errorf("Cannot verify UU process %d: %w", pid, err)
		}
		rawPath, err := queryHandleImagePath(h)
		if err != nil {
			_ = windows.CloseHandle(h)
			return "", 0, err
		}
		if (access & windows.SYNCHRONIZE) == 0 {
			_ = windows.CloseHandle(h)
			h = 0
		}
		return strings.ToLower(rawPath), h, nil
	})
	return uuSnap, handles, gameActive, err
}

func scanUUWithHandles(openRunningHandles bool) (uuSnapshot, []windows.Handle, error) {
	snap, handles, _, err := scanProcesses(openRunningHandles, false)
	return snap, handles, err
}

func scanUUAndGame(checkGame bool) (uuSnapshot, bool, error) {
	snap, _, gameActive, err := scanProcesses(false, checkGame)
	return snap, gameActive, err
}

func scanUU() (uuSnapshot, error) {
	snap, _, err := scanUUWithHandles(false)
	return snap, err
}

type heartbeat struct {
	PID      uint32 `json:"pid"`
	At       int64  `json:"at"`
	Started  uint64 `json:"started"`
	Token    string `json:"token"`
	Status   string `json:"status"`
	Error    string `json:"error"`
	UUExitAt int64  `json:"uuExitAt,omitempty"`
}

type monitorIdentity struct {
	PID     uint32
	Started uint64
	Token   string
}

const monitorStopWait = 10 * time.Second

func (h heartbeat) identity() monitorIdentity {
	return monitorIdentity{PID: h.PID, Started: h.Started, Token: h.Token}
}

func filetimeValue(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

func processStartedAt(handle windows.Handle) (uint64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	return filetimeValue(created), nil
}

func randomMonitorToken() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func newMonitorIdentity() (monitorIdentity, error) {
	started, err := processStartedAt(windows.CurrentProcess())
	if err != nil {
		return monitorIdentity{}, err
	}
	token, err := randomMonitorToken()
	if err != nil {
		return monitorIdentity{}, err
	}
	return monitorIdentity{PID: uint32(os.Getpid()), Started: started, Token: token}, nil
}

func validMonitorToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func writeJSONAtomically(path string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	tmp := path + "." + hex.EncodeToString(suffix[:]) + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeHeartbeat(identity monitorIdentity, status string, err error) error {
	wErr := writeHeartbeatTo(dataDirectory(), identity, status, err, time.Now())
	if wErr == nil {
		signalMonitorStateChanged()
	}
	return wErr
}

func writeHeartbeatTo(dir string, identity monitorIdentity, status string, err error, now time.Time) error {
	h := heartbeat{
		PID: identity.PID, At: now.Unix(), Started: identity.Started,
		Token: identity.Token, Status: status,
	}
	if err != nil {
		h.Error = err.Error()
	}
	return writeJSONAtomically(filepath.Join(dir, "monitor.json"), h)
}

func writeUUExitCompletion(identity monitorIdentity, at int64) error {
	wErr := writeUUExitCompletionTo(dataDirectory(), identity, at, time.Now())
	if wErr == nil {
		signalMonitorStateChanged()
	}
	return wErr
}

func writeUUExitCompletionTo(dir string, identity monitorIdentity, at int64, now time.Time) error {
	h := heartbeat{
		PID: identity.PID, At: now.Unix(), Started: identity.Started,
		Token: identity.Token, Status: "stopped", UUExitAt: at,
	}
	return writeJSONAtomically(filepath.Join(dir, "monitor.json"), h)
}

func completedUUExitAt(h heartbeat) int64 {
	if h.PID == 0 || h.Started == 0 || !validMonitorToken(h.Token) || h.Status != "stopped" || h.UUExitAt <= 0 {
		return 0
	}
	return h.UUExitAt
}

func completionForUUState(state uuSnapshot, scanErr error, completionAt int64) int64 {
	if scanErr != nil || state.Running || state.Updating {
		return 0
	}
	return completionAt
}

func decodeHeartbeat(b []byte) (heartbeat, error) {
	var h heartbeat
	if err := json.Unmarshal(b, &h); err != nil {
		return h, fmt.Errorf("Invalid monitor heartbeat JSON: %w", err)
	}
	if h.PID == 0 || h.Started == 0 || !validMonitorToken(h.Token) {
		return h, fmt.Errorf("Monitor heartbeat has an invalid process identity (pid=%d, started=%d, tokenLength=%d, tokenValid=%t)", h.PID, h.Started, len(h.Token), validMonitorToken(h.Token))
	}
	return h, nil
}

func approvedMonitorExecutable(path string) bool {
	exe, _ := os.Executable()
	if exe != "" && strings.EqualFold(path, exe) {
		return true
	}
	sid, err := currentUserSIDString()
	if err != nil {
		return false
	}
	return strings.EqualFold(path, protectedMonitorPathForSID(sid)) ||
		strings.EqualFold(path, legacyProtectedMonitorPathForSID(sid))
}

func verifyHeartbeat() (*heartbeat, error) {
	b, e := os.ReadFile(filepath.Join(dataDirectory(), "monitor.json"))
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("Could not read monitor heartbeat: %w", e)
	}
	h, e := decodeHeartbeat(b)
	if e != nil {
		return nil, fmt.Errorf("%w; file=%s; bytes=%d", e, filepath.Join(dataDirectory(), "monitor.json"), len(b))
	}
	age := time.Since(time.Unix(h.At, 0))
	if age < -5*time.Second {
		return nil, fmt.Errorf("Monitor heartbeat timestamp is in the future by %s", (-age).Round(time.Second))
	}
	handle, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, h.PID)
	if e != nil {
		// Fall back without SYNCHRONIZE if restricted.
		handle, e = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, h.PID)
		if e != nil {
			return nil, fmt.Errorf("Could not open monitor process %d: %w", h.PID, e)
		}
	}
	defer windows.CloseHandle(handle)
	started, e := processStartedAt(handle)
	if e != nil || started != h.Started {
		if e != nil {
			return nil, fmt.Errorf("Could not verify monitor process %d start time: %w", h.PID, e)
		}
		return nil, errors.New("Monitor heartbeat process start time does not match")
	}
	path, e := queryHandleImagePath(handle)
	if e != nil {
		return nil, fmt.Errorf("Could not verify monitor process image: %w", e)
	}
	if !approvedMonitorExecutable(path) {
		return nil, fmt.Errorf("Monitor heartbeat process image is not an approved path: %s", path)
	}
	if h.Status != "stopped" {
		if mutexExists, mErr := monitorMutexExists(); mErr == nil && !mutexExists {
			return nil, errors.New("Monitor process no longer holds the monitor mutex")
		}
	}
	return &h, nil
}

func verifiedHeartbeat() *heartbeat {
	h, _ := verifyHeartbeat()
	return h
}

func readHeartbeat() *heartbeat {
	h := verifiedHeartbeat()
	if h == nil || h.Status == "stopped" {
		return nil
	}
	return h
}

func readHeartbeatForStatus() (*heartbeat, error) {
	h, err := verifyHeartbeat()
	if err == nil {
		return h, nil
	}
	b, readErr := os.ReadFile(filepath.Join(dataDirectory(), "monitor.json"))
	if readErr == nil {
		var stopped heartbeat
		if json.Unmarshal(b, &stopped) == nil && stopped.Status == "stopped" &&
			stopped.PID != 0 && stopped.Started != 0 && validMonitorToken(stopped.Token) {
			return &stopped, nil
		}
	}
	return nil, err
}

func monitorMutexExists() (bool, error) {
	name, err := windows.UTF16PtrFromString(kernelObjectName("AutoMonitor"))
	if err != nil {
		return false, err
	}
	handle, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return false, nil
		}
		return false, err
	}
	windows.CloseHandle(handle)
	return true, nil
}

func canConfirmNoMonitor(mutexExists bool, active, stopped *heartbeat) bool {
	return !mutexExists && active == nil && stopped == nil
}

func openVerifiedMonitorProcess(identity monitorIdentity) (windows.Handle, bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, identity.PID)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return 0, false, nil
		}
		return 0, false, err
	}
	started, err := processStartedAt(handle)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return 0, false, err
	}
	if started != identity.Started {
		_ = windows.CloseHandle(handle)
		return 0, false, nil
	}
	return handle, true, nil
}

type stopWatchDeps struct {
	stopInProcess func()
	readHB        func() *heartbeat
	mutexExists   func() (bool, error)
	openProc      func(monitorIdentity) (windows.Handle, bool, error)
	signalStop    func()
	waitProc      func(windows.Handle, uint32) (uint32, error)
	closeProc     func(windows.Handle) error
	selfPID       uint32
	timeout       time.Duration
}

func stopWatchWith(d stopWatchDeps) error {
	d.stopInProcess()

	hb := d.readHB()
	var active, stopped *heartbeat
	if hb != nil {
		if hb.Status == "stopped" {
			stopped = hb
		} else {
			active = hb
		}
	}
	mutexExists, err := d.mutexExists()
	if err != nil {
		return fmt.Errorf("Could not verify monitor mutex: %w", err)
	}
	if canConfirmNoMonitor(mutexExists, active, stopped) || !mutexExists {
		return nil
	}

	var identity monitorIdentity
	if active != nil {
		identity = active.identity()
	} else if stopped != nil {
		identity = stopped.identity()
	} else {
		return errors.New("Monitor mutex exists without a verified heartbeat; stop was not confirmed")
	}

	if identity.PID == d.selfPID {
		if mExists, mErr := d.mutexExists(); mErr == nil && !mExists {
			return nil
		}
		return errors.New("In-process monitor did not release its mutex")
	}

	hProc, alive, err := d.openProc(identity)
	if err != nil {
		return fmt.Errorf("Could not open monitor process %d: %w", identity.PID, err)
	}
	if !alive {
		if mExists, mErr := d.mutexExists(); mErr == nil && !mExists {
			return nil
		}
		return errors.New("Monitor process exited without releasing the monitor mutex")
	}
	defer d.closeProc(hProc)

	if active != nil {
		d.signalStop()
	}

	deadline := time.Now().Add(d.timeout)
	for {
		rem := time.Until(deadline)
		if rem <= 0 {
			return fmt.Errorf("Monitor stop was not confirmed within %s", d.timeout)
		}
		stepMs := uint32(50)
		if rem < 50*time.Millisecond {
			stepMs = uint32((rem + time.Millisecond - 1) / time.Millisecond)
			if stepMs < 1 {
				stepMs = 1
			}
		}
		waitRes, err := d.waitProc(hProc, stepMs)
		if err != nil {
			return fmt.Errorf("Could not wait for monitor exit: %w", err)
		}
		if waitRes == uint32(windows.WAIT_OBJECT_0) {
			if mExists, mErr := d.mutexExists(); mErr == nil && mExists {
				return errors.New("Monitor process exited but the monitor mutex is still held")
			}
			return nil
		}
		if waitRes != uint32(windows.WAIT_TIMEOUT) {
			return fmt.Errorf("Unexpected monitor wait result %d", waitRes)
		}
		// Elevated --tray hosts the monitor in-process: it releases AutoMonitor and writes
		// "stopped" without terminating the resident tray process itself.
		if mExists, mErr := d.mutexExists(); mErr == nil && !mExists {
			cur := d.readHB()
			if cur == nil || cur.Status == "stopped" || cur.Token != identity.Token {
				return nil
			}
		}
	}
}

func stopUUWatch() error {
	return stopWatchWith(stopWatchDeps{
		stopInProcess: stopInProcessMonitor,
		readHB:        verifiedHeartbeat,
		mutexExists:   monitorMutexExists,
		openProc:      openVerifiedMonitorProcess,
		signalStop:    signalMonitorStop,
		waitProc:      windows.WaitForSingleObject,
		closeProc:     windows.CloseHandle,
		selfPID:       uint32(os.Getpid()),
		timeout:       monitorStopWait,
	})
}

type autoPreferences struct {
	AutoDisable bool `json:"autoDisable"`
	AutoExit    bool `json:"autoExit"`
}

func readAutoPreferences() (autoPreferences, error) {
	var preferences autoPreferences
	b, e := os.ReadFile(filepath.Join(dataDirectory(), "settings.json"))
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return preferences, nil
		}
		return preferences, e
	}
	if e = json.Unmarshal(b, &preferences); e != nil {
		return preferences, e
	}
	return preferences, nil
}

func recheckUUExitWith(deps monitorDeps) (autoPreferences, bool, error) {
	preferences, err := deps.readPreferences()
	if err != nil {
		return preferences, false, err
	}
	process, err := deps.scanUU()
	if err != nil {
		return preferences, false, err
	}
	return preferences, !process.Running && !process.Updating, nil
}

func autoExitAllowed(preferences autoPreferences, disableAttempted bool, disableErr error) bool {
	return preferences.AutoExit && (!preferences.AutoDisable || (disableAttempted && disableErr == nil))
}

type confirmedExitDecision struct {
	StopMonitor bool
	UUExit      bool
	Retry       bool
	VisibleErr  error
}

func handleConfirmedUUExit(identity monitorIdentity, deps monitorDeps) (confirmedExitDecision, error) {
	preferences, gone, err := recheckUUExitWith(deps)
	if err != nil {
		return confirmedExitDecision{Retry: true, VisibleErr: err}, nil
	}
	if !gone {
		return confirmedExitDecision{Retry: true}, nil
	}

	stopping, err := deps.honorStop(identity)
	if err != nil {
		return confirmedExitDecision{}, err
	}
	if stopping {
		return confirmedExitDecision{StopMonitor: true}, nil
	}

	disableAttempted := false
	var disableErr error
	if preferences.AutoDisable {
		preferences, gone, err = recheckUUExitWith(deps)
		if err != nil {
			return confirmedExitDecision{Retry: true, VisibleErr: err}, nil
		}
		if !gone {
			return confirmedExitDecision{Retry: true}, nil
		}
		if preferences.AutoDisable {
			disableAttempted = true
			disableErr = deps.runHelper("disable")
			if disableErr != nil {
				return confirmedExitDecision{VisibleErr: disableErr}, nil
			}
		}
	}

	stopping, err = deps.honorStop(identity)
	if err != nil {
		return confirmedExitDecision{}, err
	}
	if stopping {
		return confirmedExitDecision{StopMonitor: true}, nil
	}
	preferences, gone, err = recheckUUExitWith(deps)
	if err != nil {
		return confirmedExitDecision{Retry: true, VisibleErr: err}, nil
	}
	if !gone {
		return confirmedExitDecision{Retry: true}, nil
	}

	if preferences.AutoDisable && !disableAttempted {
		return confirmedExitDecision{Retry: true}, nil
	}
	if !autoExitAllowed(preferences, disableAttempted, disableErr) {
		return confirmedExitDecision{}, nil
	}

	stopping, err = deps.honorStop(identity)
	if err != nil {
		return confirmedExitDecision{}, err
	}
	if stopping {
		return confirmedExitDecision{StopMonitor: true}, nil
	}
	if err := deps.writeCompletion(identity, deps.now().UnixMilli()); err != nil {
		return confirmedExitDecision{}, err
	}
	return confirmedExitDecision{StopMonitor: true, UUExit: true}, nil
}

type monitorDeps struct {
	now             func() time.Time
	sleep           func(time.Duration)
	waitState       func(state string, waitHint time.Duration)
	scanUU          func() (uuSnapshot, error)
	readPreferences func() (autoPreferences, error)
	runHelper       func(string) error
	honorStop       func(monitorIdentity) (bool, error)
	writeHeartbeat  func(monitorIdentity, string, error) error
	writeCompletion func(monitorIdentity, int64) error
}

func defaultMonitorDeps() monitorDeps {
	return monitorDeps{
		now:             time.Now,
		sleep:           time.Sleep,
		scanUU:          scanUU,
		readPreferences: readAutoPreferences,
		runHelper:       runHelper,
		honorStop:       func(monitorIdentity) (bool, error) { return false, nil },
		writeHeartbeat:  writeHeartbeat,
		writeCompletion: writeUUExitCompletion,
	}
}

func runMonitorLoop(identity monitorIdentity, deps monitorDeps) (bool, error) {
	if err := deps.writeHeartbeat(identity, "starting", nil); err != nil {
		return false, err
	}
	var detector exitDetector
	var lastError error
	actionErrSticky := false
	prevPublishedState := "starting"
	prevPublishedErr := ""

	for {
		stopping, err := deps.honorStop(identity)
		if err != nil {
			return false, err
		}
		if stopping {
			return false, nil
		}
		preferences, err := deps.readPreferences()
		if err != nil {
			return false, fmt.Errorf("Monitor stopped: invalid settings: %w", err)
		}
		if !preferences.AutoDisable && !preferences.AutoExit {
			return false, nil
		}
		process, err := deps.scanUU()
		state := "waiting"
		if err != nil {
			detector.AbsentSince = time.Time{}
			state = "error"
			lastError = err
			actionErrSticky = false
		} else {
			if !actionErrSticky {
				lastError = nil
			}
			if process.Running {
				state = "armed"
				lastError = nil
				actionErrSticky = false
			} else if detector.Seen && !detector.Handled {
				state = "grace"
			} else if detector.Handled {
				state = "done"
			}
			if process.Updating {
				state = "updating"
			}
			if detector.Observe(process, deps.now()) {
				decision, err := handleConfirmedUUExit(identity, deps)
				if err != nil {
					return false, err
				}
				if decision.StopMonitor {
					return decision.UUExit, nil
				}
				if decision.Retry {
					detector.Handled = false
					detector.AbsentSince = time.Time{}
					state = "grace"
				}
				if decision.VisibleErr != nil {
					lastError = decision.VisibleErr
					state = "error"
					actionErrSticky = !decision.Retry
				} else if !decision.Retry {
					lastError = nil
					actionErrSticky = false
					state = "done"
				}
			}
			if lastError != nil {
				state = "error"
			}
		}

		errText := ""
		if lastError != nil {
			errText = lastError.Error()
		}
		// Publish to disk only when state or error changes, eliminating periodic SSD writes.
		if state != prevPublishedState || errText != prevPublishedErr {
			if err = deps.writeHeartbeat(identity, state, lastError); err != nil {
				return false, err
			}
			prevPublishedState = state
			prevPublishedErr = errText
		}

		var waitHint time.Duration
		if state == "grace" && !detector.AbsentSince.IsZero() {
			waitHint = 6*time.Second - deps.now().Sub(detector.AbsentSince)
			if waitHint < time.Millisecond {
				waitHint = time.Millisecond
			}
		}
		if deps.waitState != nil {
			deps.waitState(state, waitHint)
		} else {
			deps.sleep(2 * time.Second)
		}
	}
}

func runEventDrivenMonitor(localStop <-chan struct{}, onReady func(error)) (bool, error) {
	signalReady := func(err error) {
		if onReady != nil {
			onReady(err)
			onReady = nil
		}
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !elevated() {
		err := errors.New("Monitor needs administrator permission for later automatic device operations")
		signalReady(err)
		return false, err
	}
	name, _ := windows.UTF16PtrFromString(kernelObjectName("AutoMonitor"))
	sa, saErr := buildCrossIntegritySA(mutexAllAccess)
	if saErr != nil {
		sa = nil
	}
	mutex, e := windows.CreateMutex(sa, true, name)
	runtime.KeepAlive(sa)
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		signalReady(nil)
		return false, nil
	}
	if e != nil {
		signalReady(e)
		return false, e
	}
	defer windows.ReleaseMutex(mutex)

	if e = os.MkdirAll(dataDirectory(), 0700); e != nil {
		signalReady(e)
		return false, e
	}
	identity, e := newMonitorIdentity()
	if e != nil {
		err := fmt.Errorf("Could not identify monitor process: %w", e)
		signalReady(err)
		return false, err
	}

	stopEvent, _ := createOrOpenSharedEvent("MonitorStop", false)
	if stopEvent != 0 {
		// Drain any stale stop signal from an earlier monitor lifecycle.
		_, _ = windows.WaitForSingleObject(stopEvent, 0)
		defer windows.CloseHandle(stopEvent)
	}
	wakeEvent, _ := createOrOpenSharedEvent("MonitorWake", false)
	if wakeEvent != 0 {
		defer windows.CloseHandle(wakeEvent)
	}

	signalReady(nil)

	var trackedHandles []windows.Handle
	closeTracked := func() {
		for _, h := range trackedHandles {
			_ = windows.CloseHandle(h)
		}
		trackedHandles = nil
	}
	defer closeTracked()

	stopSignaled := false
	deps := defaultMonitorDeps()
	deps.scanUU = func() (uuSnapshot, error) {
		closeTracked()
		snap, handles, err := scanUUWithHandles(true)
		trackedHandles = handles
		return snap, err
	}
	deps.honorStop = func(id monitorIdentity) (bool, error) {
		if localStop != nil {
			select {
			case <-localStop:
				return true, nil
			default:
			}
		}
		if stopSignaled || (stopEvent != 0 && func() bool {
			r, err := windows.WaitForSingleObject(stopEvent, 0)
			return err == nil && r == windows.WAIT_OBJECT_0
		}()) {
			return true, nil
		}
		return false, nil
	}
	deps.waitState = func(state string, waitHint time.Duration) {
		if localStop != nil {
			select {
			case <-localStop:
				return
			default:
			}
		}
		var waitHandles []windows.Handle
		if stopEvent != 0 {
			waitHandles = append(waitHandles, stopEvent)
		}
		if wakeEvent != 0 {
			waitHandles = append(waitHandles, wakeEvent)
		}
		timeoutMs := uint32(15000)
		switch state {
		case "armed", "updating":
			if len(trackedHandles) > 0 {
				waitHandles = append(waitHandles, trackedHandles...)
				// Block with 0% CPU until ANY tracked GameViewer/updater exits or stop/wake is signaled.
				timeoutMs = windows.INFINITE
			} else if state == "updating" {
				timeoutMs = 1000
			} else {
				timeoutMs = 5000
			}
		case "grace":
			if waitHint > 0 {
				ms := uint32((waitHint + time.Millisecond - 1) / time.Millisecond)
				if ms < 1 {
					ms = 1
				}
				timeoutMs = ms
			} else {
				timeoutMs = 1000
			}
		}
		if len(waitHandles) > 0 {
			res, _ := windows.WaitForMultipleObjects(waitHandles, false, timeoutMs)
			if stopEvent != 0 && waitHandles[0] == stopEvent && res == windows.WAIT_OBJECT_0 {
				stopSignaled = true
			}
		} else {
			time.Sleep(time.Duration(timeoutMs) * time.Millisecond)
		}
	}

	exitCompleted := false
	defer func() {
		if !exitCompleted {
			_ = writeHeartbeat(identity, "stopped", e)
		}
	}()
	exitCompleted, e = runMonitorLoop(identity, deps)
	return exitCompleted, e
}

func watchUU() error {
	_, err := runEventDrivenMonitor(nil, nil)
	return err
}

var (
	monStateMu     sync.Mutex
	monStateActive bool
	monStateCh     = make(chan struct{})
)

func setInProcessMonitorStateWaiterActive(active bool) {
	monStateMu.Lock()
	monStateActive = active
	monStateMu.Unlock()
}

func notifyInProcessMonitorStateWaiters() {
	monStateMu.Lock()
	close(monStateCh)
	monStateCh = make(chan struct{})
	monStateMu.Unlock()
}

func currentMonitorStateSignal() (<-chan struct{}, bool) {
	monStateMu.Lock()
	defer monStateMu.Unlock()
	return monStateCh, monStateActive
}

var (
	inProcMonitorMu       sync.Mutex
	inProcMonitorStop     chan struct{}
	inProcMonitorDone     chan struct{}
	inProcMonitorStopping bool
)

func startInProcessMonitor(onUUAutoExit func()) (<-chan error, bool) {
	if globalReadOnly || !elevated() {
		return nil, false
	}
	for {
		inProcMonitorMu.Lock()
		if inProcMonitorDone != nil {
			select {
			case <-inProcMonitorDone:
				inProcMonitorDone = nil
				inProcMonitorStop = nil
				inProcMonitorStopping = false
			default:
				if inProcMonitorStopping || readHeartbeat() == nil {
					doneCh := inProcMonitorDone
					inProcMonitorMu.Unlock()
					select {
					case <-doneCh:
					case <-time.After(5 * time.Second):
						return nil, true
					}
					continue
				}
				inProcMonitorMu.Unlock()
				return nil, true
			}
		}
		if exists, err := monitorMutexExists(); err == nil && exists {
			if readHeartbeat() != nil {
				inProcMonitorMu.Unlock()
				return nil, true
			}
			inProcMonitorMu.Unlock()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if exists2, err2 := monitorMutexExists(); err2 == nil && !exists2 {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			inProcMonitorMu.Lock()
			if exists2, err2 := monitorMutexExists(); err2 == nil && exists2 {
				inProcMonitorMu.Unlock()
				return nil, true
			}
		}
		stopCh := make(chan struct{})
		doneCh := make(chan struct{})
		readyCh := make(chan error, 1)
		inProcMonitorStop = stopCh
		inProcMonitorDone = doneCh
		inProcMonitorStopping = false
		inProcMonitorMu.Unlock()

		go func() {
			defer close(doneCh)
			uuExit, err := runEventDrivenMonitor(stopCh, func(readyErr error) {
				readyCh <- readyErr
			})
			if err != nil {
				_ = appendActivity(activity{Time: time.Now().Format(time.RFC3339), Mode: "monitor", Message: err.Error()})
			}
			if uuExit && onUUAutoExit != nil {
				onUUAutoExit()
			}
		}()
		return readyCh, true
	}
}

func stopInProcessMonitor() {
	inProcMonitorMu.Lock()
	stopCh := inProcMonitorStop
	doneCh := inProcMonitorDone
	inProcMonitorStop = nil
	if doneCh != nil {
		inProcMonitorStopping = true
	}
	inProcMonitorMu.Unlock()

	if stopCh != nil {
		close(stopCh)
		signalMonitorStop()
	}
	if doneCh != nil {
		select {
		case <-doneCh:
		case <-time.After(5 * time.Second):
		}
		inProcMonitorMu.Lock()
		if inProcMonitorDone == doneCh {
			inProcMonitorDone = nil
			inProcMonitorStopping = false
		}
		inProcMonitorMu.Unlock()
	}
}

func ensureMonitorRunning(ownerHwnd uintptr, onUUAutoExit func()) error {
	if globalReadOnly {
		return errors.New("read-only mode rejects monitor startup")
	}
	if active := readHeartbeat(); active != nil {
		return nil
	}
	if elevated() {
		readyCh, started := startInProcessMonitor(onUUAutoExit)
		if started && readyCh != nil {
			select {
			case err := <-readyCh:
				return err
			case <-time.After(5 * time.Second):
				return nil
			}
		}
		return nil
	}

	stateCh, waiterActive := currentMonitorStateSignal()
	var monEvt windows.Handle
	if !waiterActive {
		monEvt, _ = createOrOpenSharedEvent("MonitorStateChanged", false)
		if monEvt != 0 {
			// Drain any prior pulse before launching the child so we only wake on new updates.
			_, _ = windows.WaitForSingleObject(monEvt, 0)
			defer windows.CloseHandle(monEvt)
		}
	}

	proc, err := launchElevated("--watch", ownerHwnd)
	if err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("已取消管理员授权，未启动后台监测。")
		}
		return fmt.Errorf("无法启动后台监测：%w", err)
	}
	if proc != 0 {
		defer windows.CloseHandle(proc)
	}

	var procDone chan struct{}
	if waiterActive && proc != 0 {
		stopWait, _, _ := procCreateEventW.Call(0, 1, 0, 0)
		if stopWait != 0 {
			stopWaitHandle := windows.Handle(stopWait)
			done := make(chan struct{})
			procDone = done
			go func() {
				defer close(done)
				_, _ = windows.WaitForMultipleObjects([]windows.Handle{stopWaitHandle, proc}, false, windows.INFINITE)
			}()
			defer func() {
				procSetEvent.Call(uintptr(stopWaitHandle))
				<-done
				_ = windows.CloseHandle(stopWaitHandle)
			}()
		}
	}

	startedAt := time.Now().UnixMilli()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if active := readHeartbeat(); active != nil {
			return nil
		}
		if w, hbErr := readHeartbeatForStatus(); hbErr == nil && w != nil && completedUUExitAt(*w) > startedAt {
			st := readState(false)
			if st.UUStateKnown && !st.UURunning && st.UUExitAt > startedAt {
				return nil
			}
		}
		if proc != 0 {
			if w, _ := windows.WaitForSingleObject(proc, 0); w == windows.WAIT_OBJECT_0 {
				var code uint32
				_ = windows.GetExitCodeProcess(proc, &code)
				if code != 0 {
					st := readState(false)
					if st.WatcherError != "" {
						return fmt.Errorf("后台监测进程启动失败（退出码 %d）：%s", code, st.WatcherError)
					}
					return fmt.Errorf("后台监测进程启动失败（退出码 %d）", code)
				}
				return nil
			}
		}
		rem := time.Until(deadline)
		if rem <= 0 {
			return errors.New("后台监测未在超时时间内就绪。")
		}
		if waiterActive {
			timer := time.NewTimer(rem)
			select {
			case <-stateCh:
				timer.Stop()
				stateCh, waiterActive = currentMonitorStateSignal()
			case <-procDone:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		var waitHandles []windows.Handle
		if monEvt != 0 {
			waitHandles = append(waitHandles, monEvt)
		}
		if proc != 0 {
			waitHandles = append(waitHandles, proc)
		}
		if len(waitHandles) == 0 {
			return errors.New("无法等待后台监测进程就绪。")
		}
		waitMs := uint32((rem + time.Millisecond - 1) / time.Millisecond)
		if waitMs < 1 {
			waitMs = 1
		}
		_, _ = windows.WaitForMultipleObjects(waitHandles, false, waitMs)
	}
}
