package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"
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
func scanUU() (uuSnapshot, error) {
	var result uuSnapshot
	snap, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return result, e
	}
	defer windows.CloseHandle(snap)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	e = windows.Process32First(snap, &entry)
	for e == nil {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if name == "gameviewer.exe" || name == "upgrade.exe" || name == "gameviewerlauncher.exe" {
			h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
			if err != nil {
				return result, fmt.Errorf("Cannot verify UU process %d: %w", entry.ProcessID, err)
			}
			buf := make([]uint16, 32768)
			size := uint32(len(buf))
			err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
			windows.CloseHandle(h)
			if err != nil {
				return result, err
			}
			path := strings.ToLower(windows.UTF16ToString(buf[:size]))
			root := strings.ToLower(filepath.Join(os.Getenv("ProgramFiles"), "Netease", "GameViewer")) + `\`
			if strings.HasPrefix(path, root) {
				if name == "gameviewer.exe" {
					result.Running = true
				} else {
					result.Updating = true
				}
			}
		}
		e = windows.Process32Next(snap, &entry)
	}
	if !errors.Is(e, windows.ERROR_NO_MORE_FILES) {
		return result, e
	}
	return result, nil
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

type monitorStopRequest struct {
	PID          uint32 `json:"pid"`
	Started      uint64 `json:"started"`
	MonitorToken string `json:"monitorToken"`
	RequestedAt  int64  `json:"requestedAt"`
	RequestToken string `json:"requestToken"`
}

type monitorStopAck struct {
	PID          uint32 `json:"pid"`
	Started      uint64 `json:"started"`
	MonitorToken string `json:"monitorToken"`
	RequestToken string `json:"requestToken"`
	StoppedAt    int64  `json:"stoppedAt"`
}

const (
	monitorStopRequestMaxAge = 30 * time.Second
	monitorStopWait          = 10 * time.Second
	monitorStopPoll          = 100 * time.Millisecond
	monitorHeartbeatMaxAge   = 8 * time.Second
)

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
	tmp := path + ".tmp"
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
	return writeHeartbeatTo(dataDirectory(), identity, status, err, time.Now())
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
	return writeUUExitCompletionTo(dataDirectory(), identity, at, time.Now())
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

func readUUExitCompletion() int64 {
	b, err := os.ReadFile(filepath.Join(dataDirectory(), "monitor.json"))
	if err != nil {
		return 0
	}
	var h heartbeat
	if json.Unmarshal(b, &h) != nil {
		return 0
	}
	return completedUUExitAt(h)
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
	if age < 0 {
		return nil, fmt.Errorf("Monitor heartbeat timestamp is in the future by %s", (-age).Round(time.Second))
	}
	if age > monitorHeartbeatMaxAge {
		return nil, fmt.Errorf("Monitor heartbeat is stale (%s old)", age.Round(time.Second))
	}
	handle, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, h.PID)
	if e != nil {
		return nil, fmt.Errorf("Could not open monitor process %d: %w", h.PID, e)
	}
	defer windows.CloseHandle(handle)
	started, e := processStartedAt(handle)
	if e != nil || started != h.Started {
		if e != nil {
			return nil, fmt.Errorf("Could not verify monitor process %d start time: %w", h.PID, e)
		}
		return nil, errors.New("Monitor heartbeat process start time does not match")
	}
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if e := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); e != nil {
		return nil, fmt.Errorf("Could not verify monitor process image: %w", e)
	}
	exe, _ := os.Executable()
	path := windows.UTF16ToString(buf[:size])
	if !strings.EqualFold(path, exe) && !strings.EqualFold(path, protectedMonitorPath()) {
		return nil, fmt.Errorf("Monitor heartbeat process image is not an approved path: %s", path)
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
	// A stopped record is written immediately before the monitor exits. Its
	// process may already be gone by the time the UI asks for status.
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

func readStoppedHeartbeat() *heartbeat {
	h := verifiedHeartbeat()
	if h == nil || h.Status != "stopped" {
		return nil
	}
	return h
}

func monitorStopPrefix(identity monitorIdentity) string {
	return fmt.Sprintf("monitor-stop-%d-%d-%s-", identity.PID, identity.Started, identity.Token)
}

func monitorStopRequestName(request monitorStopRequest) string {
	id := monitorIdentity{PID: request.PID, Started: request.Started, Token: request.MonitorToken}
	return monitorStopPrefix(id) + request.RequestToken + ".req.json"
}

func monitorStopAckName(request monitorStopRequest) string {
	return strings.TrimSuffix(monitorStopRequestName(request), ".req.json") + ".ack.json"
}

func (request monitorStopRequest) matches(identity monitorIdentity, now time.Time) bool {
	if request.PID != identity.PID || request.Started != identity.Started || request.MonitorToken != identity.Token ||
		!validMonitorToken(request.MonitorToken) || !validMonitorToken(request.RequestToken) {
		return false
	}
	age := now.Sub(time.Unix(0, request.RequestedAt))
	return age >= 0 && age <= monitorStopRequestMaxAge
}

func (ack monitorStopAck) matches(request monitorStopRequest) bool {
	return ack.PID == request.PID && ack.Started == request.Started &&
		ack.MonitorToken == request.MonitorToken && ack.RequestToken == request.RequestToken && ack.StoppedAt > 0
}

func findMonitorStopRequests(identity monitorIdentity, now time.Time) ([]monitorStopRequest, error) {
	entries, err := os.ReadDir(dataDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	prefix := monitorStopPrefix(identity)
	var requests []monitorStopRequest
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".req.json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dataDirectory(), name))
		if err != nil {
			return nil, err
		}
		var request monitorStopRequest
		if json.Unmarshal(b, &request) != nil || !request.matches(identity, now) || monitorStopRequestName(request) != name {
			continue
		}
		requests = append(requests, request)
	}
	return requests, nil
}

func honorMonitorStop(identity monitorIdentity) (bool, error) {
	requests, err := findMonitorStopRequests(identity, time.Now())
	if err != nil || len(requests) == 0 {
		return false, err
	}
	if err := writeHeartbeat(identity, "stopped", nil); err != nil {
		return false, err
	}
	for _, request := range requests {
		ack := monitorStopAck{
			PID: request.PID, Started: request.Started, MonitorToken: request.MonitorToken,
			RequestToken: request.RequestToken, StoppedAt: time.Now().UnixNano(),
		}
		if err := writeJSONAtomically(filepath.Join(dataDirectory(), monitorStopAckName(request)), ack); err != nil {
			return false, err
		}
	}
	return true, nil
}

func monitorIdentityRunning(identity monitorIdentity) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, identity.PID)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(handle)
	started, err := processStartedAt(handle)
	if err != nil {
		return false, err
	}
	if started != identity.Started {
		return false, nil
	}
	result, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false, err
	}
	switch result {
	case windows.WAIT_OBJECT_0:
		return false, nil
	case uint32(windows.WAIT_TIMEOUT):
		return true, nil
	default:
		return false, fmt.Errorf("Unexpected process wait result %d", result)
	}
}

func monitorMutexExists() (bool, error) {
	name, err := windows.UTF16PtrFromString(`Local\UUDisplaySwitch.AutoMonitor`)
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

func waitForMonitorExit(identity monitorIdentity) error {
	deadline := time.Now().Add(monitorStopWait)
	for {
		running, err := monitorIdentityRunning(identity)
		if err != nil {
			return fmt.Errorf("Could not verify monitor exit: %w", err)
		}
		if !running {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("Monitor stop was not confirmed within %s", monitorStopWait)
		}
		time.Sleep(monitorStopPoll)
	}
}

func stopUUWatch() error {
	active := readHeartbeat()
	stopped := readStoppedHeartbeat()
	mutexExists, err := monitorMutexExists()
	if err != nil {
		return fmt.Errorf("Could not verify monitor mutex: %w", err)
	}
	if canConfirmNoMonitor(mutexExists, active, stopped) {
		return nil
	}
	if !mutexExists {
		candidate := active
		if candidate == nil {
			candidate = stopped
		}
		if candidate != nil {
			running, err := monitorIdentityRunning(candidate.identity())
			if err != nil {
				return fmt.Errorf("Could not verify monitor process: %w", err)
			}
			if !running {
				return nil
			}
		}
		return errors.New("Monitor heartbeat exists without its mutex; stop was not confirmed")
	}
	if active == nil {
		if stopped != nil {
			return waitForMonitorExit(stopped.identity())
		}
		return errors.New("Monitor mutex exists without a verified heartbeat; stop was not confirmed")
	}
	identity := active.identity()
	requestToken, err := randomMonitorToken()
	if err != nil {
		return fmt.Errorf("Could not create monitor stop request: %w", err)
	}
	request := monitorStopRequest{
		PID: identity.PID, Started: identity.Started, MonitorToken: identity.Token,
		RequestedAt: time.Now().UnixNano(), RequestToken: requestToken,
	}
	requestPath := filepath.Join(dataDirectory(), monitorStopRequestName(request))
	ackPath := filepath.Join(dataDirectory(), monitorStopAckName(request))
	if err := writeJSONAtomically(requestPath, request); err != nil {
		return fmt.Errorf("Could not write monitor stop request: %w", err)
	}
	defer os.Remove(requestPath)
	defer os.Remove(ackPath)

	deadline := time.Now().Add(monitorStopWait)
	for {
		b, readErr := os.ReadFile(ackPath)
		if readErr == nil {
			var ack monitorStopAck
			if json.Unmarshal(b, &ack) == nil && ack.matches(request) {
				running, err := monitorIdentityRunning(identity)
				if err != nil {
					return fmt.Errorf("Could not verify monitor exit: %w", err)
				}
				if !running {
					return nil
				}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("Could not read monitor stop confirmation: %w", readErr)
		}

		running, err := monitorIdentityRunning(identity)
		if err != nil {
			return fmt.Errorf("Could not verify monitor process: %w", err)
		}
		if !running {
			return errors.New("Monitor process exited without acknowledging the stop request")
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("Monitor stop was not confirmed within %s", monitorStopWait)
		}
		time.Sleep(monitorStopPoll)
	}
}
func protectedMonitorPath() string {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ""
	}
	return filepath.Join(os.Getenv("ProgramFiles"), "UUDisplaySwitch", user.User.Sid.String(), "UUDisplayCore.exe")
}

type autoPreferences struct {
	AutoDisable bool `json:"autoDisable"`
	AutoExit    bool `json:"autoExit"`
}

func readAutoPreferences() (autoPreferences, error) {
	var preferences autoPreferences
	b, e := os.ReadFile(filepath.Join(dataDirectory(), "settings.json"))
	if e != nil {
		return preferences, e
	}
	if e = json.Unmarshal(b, &preferences); e != nil {
		return preferences, e
	}
	return preferences, nil
}

func recheckUUExit() (autoPreferences, bool, error) {
	return recheckUUExitWith(defaultMonitorDeps())
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
		// Recheck both preferences and UU immediately before the device action.
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

	// Recheck stop requests before the final preference/process check so a
	// manual stop cannot publish the UU-exit completion timestamp.
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

	// Check one last time for a concurrent manual stop before publishing the
	// UU-exit completion; the preference/process check above remains the gate.
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

// monitorDeps keeps the production monitor behavior in one path while allowing
// lifecycle tests to isolate process scans, device actions, stop signals, time,
// and heartbeat files without creating the Windows resident mutex.
type monitorDeps struct {
	now             func() time.Time
	sleep           func(time.Duration)
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
		honorStop:       honorMonitorStop,
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
		} else {
			if process.Running {
				state = "armed"
				lastError = nil
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
				} else if !decision.Retry {
					lastError = nil
					state = "done"
				}
			}
			if lastError != nil {
				state = "error"
			}
		}
		if err = deps.writeHeartbeat(identity, state, lastError); err != nil {
			return false, err
		}
		deps.sleep(2 * time.Second)
	}
}

func watchUU() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !elevated() {
		return errors.New("Monitor needs administrator permission for later automatic device operations")
	}
	name, _ := windows.UTF16PtrFromString(`Local\UUDisplaySwitch.AutoMonitor`)
	mutex, e := windows.CreateMutex(nil, true, name)
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		return nil
	}
	if e != nil {
		return e
	}
	defer windows.ReleaseMutex(mutex)
	if e = os.MkdirAll(dataDirectory(), 0700); e != nil {
		return e
	}
	identity, e := newMonitorIdentity()
	if e != nil {
		return fmt.Errorf("Could not identify monitor process: %w", e)
	}
	exitCompleted := false
	defer func() {
		if !exitCompleted {
			_ = writeHeartbeat(identity, "stopped", nil)
		}
	}()
	exitCompleted, e = runMonitorLoop(identity, defaultMonitorDeps())
	return e
}
