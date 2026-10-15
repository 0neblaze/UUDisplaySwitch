package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type monitorScanResult struct {
	state uuSnapshot
	err   error
}

type monitorLifecycleHarness struct {
	now             time.Time
	start           time.Time
	stopAfter       time.Duration
	preferences     autoPreferences
	scans           []monitorScanResult
	fallback        monitorScanResult
	scanIndex       int
	firstAbsentAt   time.Time
	disableErr      error
	disableCalls    []string
	disableAt       time.Time
	completionCalls int
	completionAt    time.Time
	dataDir         string
	lastHeartbeat   heartbeat
	deps            monitorDeps
}

func newMonitorLifecycleHarness(t *testing.T, preferences autoPreferences, scans []monitorScanResult, fallback monitorScanResult, stopAfter time.Duration, disableErr error) *monitorLifecycleHarness {
	t.Helper()
	start := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	h := &monitorLifecycleHarness{
		now:         start,
		start:       start,
		stopAfter:   stopAfter,
		preferences: preferences,
		scans:       append([]monitorScanResult(nil), scans...),
		fallback:    fallback,
		disableErr:  disableErr,
		dataDir:     t.TempDir(),
	}
	h.deps = defaultMonitorDeps()
	h.deps.now = func() time.Time { return h.now }
	h.deps.sleep = func(d time.Duration) { h.now = h.now.Add(d) }
	h.deps.readPreferences = func() (autoPreferences, error) { return h.preferences, nil }
	h.deps.scanUU = func() (uuSnapshot, error) {
		result := h.fallback
		if h.scanIndex < len(h.scans) {
			result = h.scans[h.scanIndex]
		}
		h.scanIndex++
		if result.err == nil && !result.state.Running && !result.state.Updating && h.firstAbsentAt.IsZero() {
			h.firstAbsentAt = h.now
		}
		return result.state, result.err
	}
	h.deps.runHelper = func(mode string) error {
		h.disableCalls = append(h.disableCalls, mode)
		h.disableAt = h.now
		return h.disableErr
	}
	h.deps.honorStop = func(monitorIdentity) (bool, error) {
		return h.now.Sub(h.start) >= h.stopAfter, nil
	}
	h.deps.writeHeartbeat = func(identity monitorIdentity, status string, err error) error {
		if writeErr := writeHeartbeatTo(h.dataDir, identity, status, err, h.now); writeErr != nil {
			return writeErr
		}
		b, readErr := os.ReadFile(filepath.Join(h.dataDir, "monitor.json"))
		if readErr != nil {
			return readErr
		}
		return json.Unmarshal(b, &h.lastHeartbeat)
	}
	h.deps.writeCompletion = func(identity monitorIdentity, at int64) error {
		h.completionCalls++
		h.completionAt = h.now
		return writeUUExitCompletionTo(h.dataDir, identity, at, h.now)
	}
	return h
}

func (h *monitorLifecycleHarness) run(identity monitorIdentity) (bool, error) {
	return runMonitorLoop(identity, h.deps)
}

func testMonitorIdentity() monitorIdentity {
	return monitorIdentity{
		PID:     4242,
		Started: 134365279482735989,
		Token:   "0123456789abcdef0123456789abcdef",
	}
}

func TestMonitorLoopStaysArmedWhileUUContinuesRunning(t *testing.T) {
	running := monitorScanResult{state: uuSnapshot{Running: true}}
	h := newMonitorLifecycleHarness(t, autoPreferences{AutoExit: true, AutoDisable: true}, nil, running, 10*time.Second, nil)
	completed, err := h.run(testMonitorIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if completed {
		t.Fatal("continuous UU process was treated as a completed exit")
	}
	if h.scanIndex != 5 {
		t.Fatalf("UU scan count = %d, want 5 running observations", h.scanIndex)
	}
	if len(h.disableCalls) != 0 || h.completionCalls != 0 {
		t.Fatalf("running UU caused device/exit actions: disable=%v completion=%d", h.disableCalls, h.completionCalls)
	}
	if h.lastHeartbeat.Status != "armed" {
		t.Fatalf("last heartbeat status = %q, want armed", h.lastHeartbeat.Status)
	}
}

func TestMonitorLoopAutomationPreferenceCombinations(t *testing.T) {
	cases := []struct {
		name        string
		preferences autoPreferences
		wantDisable int
		wantExit    bool
	}{
		{name: "neither option", preferences: autoPreferences{}},
		{name: "auto-exit only", preferences: autoPreferences{AutoExit: true}, wantExit: true},
		{name: "auto-disable only", preferences: autoPreferences{AutoDisable: true}, wantDisable: 1},
		{name: "both options", preferences: autoPreferences{AutoDisable: true, AutoExit: true}, wantDisable: 1, wantExit: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scans := []monitorScanResult{{state: uuSnapshot{Running: true}}, {state: uuSnapshot{}}}
			h := newMonitorLifecycleHarness(t, tc.preferences, scans, monitorScanResult{}, 10*time.Second, nil)
			completed, err := h.run(testMonitorIdentity())
			if err != nil {
				t.Fatal(err)
			}
			if completed != tc.wantExit {
				t.Fatalf("monitor completion = %v, want %v", completed, tc.wantExit)
			}
			if len(h.disableCalls) != tc.wantDisable {
				t.Fatalf("disable calls = %v, want %d", h.disableCalls, tc.wantDisable)
			}
			if tc.wantDisable != 0 && h.disableAt.Sub(h.firstAbsentAt) < 6*time.Second {
				t.Fatalf("device action followed first UU absence after %s, want at least 6s", h.disableAt.Sub(h.firstAbsentAt))
			}
			if tc.wantExit {
				if h.completionCalls != 1 {
					t.Fatalf("completion writes = %d, want 1", h.completionCalls)
				}
				if h.completionAt.Sub(h.firstAbsentAt) < 6*time.Second {
					t.Fatalf("completion followed first UU absence after %s, want at least 6s", h.completionAt.Sub(h.firstAbsentAt))
				}
				b, readErr := os.ReadFile(filepath.Join(h.dataDir, "monitor.json"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				var completedHeartbeat heartbeat
				if err := json.Unmarshal(b, &completedHeartbeat); err != nil {
					t.Fatal(err)
				}
				identity := testMonitorIdentity()
				if completedHeartbeat.Status != "stopped" || completedHeartbeat.UUExitAt != h.completionAt.UnixMilli() ||
					completedHeartbeat.PID != identity.PID || completedHeartbeat.Started != identity.Started || completedHeartbeat.Token != identity.Token {
					t.Fatalf("completion heartbeat lost status, timestamp, or identity: %+v", completedHeartbeat)
				}
			} else if h.completionCalls != 0 {
				t.Fatalf("completion writes = %d with AutoExit disabled", h.completionCalls)
			}
			if tc.preferences.AutoDisable || tc.preferences.AutoExit {
				if h.firstAbsentAt.IsZero() {
					t.Fatal("UU absence was not observed")
				}
			} else if h.scanIndex != 0 {
				t.Fatalf("disabled automation unexpectedly scanned UU %d times", h.scanIndex)
			}
		})
	}
}

func TestMonitorLoopDisableFailureNeverPublishesCompletion(t *testing.T) {
	scans := []monitorScanResult{{state: uuSnapshot{Running: true}}, {state: uuSnapshot{}}}
	h := newMonitorLifecycleHarness(t, autoPreferences{AutoDisable: true, AutoExit: true}, scans, monitorScanResult{}, 10*time.Second, errors.New("fake disable failure"))
	completed, err := h.run(testMonitorIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if completed || len(h.disableCalls) != 1 || h.completionCalls != 0 {
		t.Fatalf("failed disable result: completed=%v disable=%v completion=%d", completed, h.disableCalls, h.completionCalls)
	}
	if h.lastHeartbeat.Status != "error" || h.lastHeartbeat.Error != "fake disable failure" {
		t.Fatalf("disable failure not retained in heartbeat: %+v", h.lastHeartbeat)
	}
}

func TestMonitorLoopResetsDebounceForRestartUpdateAndEnumerationFailure(t *testing.T) {
	cases := []struct {
		name  string
		scans []monitorScanResult
	}{
		{
			name: "quick restart",
			scans: []monitorScanResult{
				{state: uuSnapshot{Running: true}},
				{state: uuSnapshot{}},
				{state: uuSnapshot{Running: true}},
				{state: uuSnapshot{}},
				{state: uuSnapshot{}},
			},
		},
		{
			name: "updating",
			scans: []monitorScanResult{
				{state: uuSnapshot{Running: true}},
				{state: uuSnapshot{}},
				{state: uuSnapshot{Updating: true}},
				{state: uuSnapshot{}},
				{state: uuSnapshot{}},
			},
		},
		{
			name: "process enumeration failure",
			scans: []monitorScanResult{
				{state: uuSnapshot{Running: true}},
				{state: uuSnapshot{}},
				{err: errors.New("fake process enumeration failure")},
				{state: uuSnapshot{}},
				{state: uuSnapshot{}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newMonitorLifecycleHarness(t, autoPreferences{AutoExit: true, AutoDisable: true}, tc.scans, monitorScanResult{}, 10*time.Second, nil)
			completed, err := h.run(testMonitorIdentity())
			if err != nil {
				t.Fatal(err)
			}
			if completed || len(h.disableCalls) != 0 || h.completionCalls != 0 {
				t.Fatalf("transient state triggered action: completed=%v disable=%v completion=%d", completed, h.disableCalls, h.completionCalls)
			}
			if tc.name == "process enumeration failure" && h.lastHeartbeat.Error != "fake process enumeration failure" {
				t.Fatalf("enumeration error was not preserved: %+v", h.lastHeartbeat)
			}
		})
	}
}
