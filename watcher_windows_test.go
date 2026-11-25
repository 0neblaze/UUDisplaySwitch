package main

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestHeartbeatIdentityValidation(t *testing.T) {
	valid := heartbeat{PID: 64036, Started: 134365279482735989, Token: "3ce7ac0edd6bb01e8cb31fe4315a9477", Status: "armed"}
	b, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeHeartbeat(b)
	if err != nil || decoded.Started != valid.Started {
		t.Fatalf("valid identity rejected or rounded: %v, %v", decoded, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*heartbeat)
		detail string
	}{
		{"missing pid", func(h *heartbeat) { h.PID = 0 }, "pid=0"},
		{"missing start", func(h *heartbeat) { h.Started = 0 }, "started=0"},
		{"missing token", func(h *heartbeat) { h.Token = "" }, "tokenLength=0"},
		{"nonhex token", func(h *heartbeat) { h.Token = strings.Repeat("x", 32) }, "tokenValid=false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := valid
			tc.change(&h)
			b, _ := json.Marshal(h)
			_, err := decodeHeartbeat(b)
			if err == nil || !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("missing identity diagnosis %s: %v", tc.detail, err)
			}
		})
	}
	if _, err := decodeHeartbeat([]byte(`{"pid":`)); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("invalid JSON accepted: %v", err)
	}
}

func TestExitDetectorLifecycle(t *testing.T) {
	var d exitDetector
	n := time.Now()
	if d.Observe(uuSnapshot{}, n) {
		t.Fatal("startup absence triggered")
	}
	d.Observe(uuSnapshot{Running: true}, n)
	for i := 0; i < 10; i++ {
		if d.Observe(uuSnapshot{Running: true}, n.Add(time.Duration(i)*time.Second)) {
			t.Fatal("running/minimized triggered")
		}
	}
	if d.Observe(uuSnapshot{}, n.Add(10*time.Second)) {
		t.Fatal("no debounce")
	}
	if d.Observe(uuSnapshot{}, n.Add(15*time.Second)) {
		t.Fatal("too soon")
	}
	if !d.Observe(uuSnapshot{}, n.Add(16*time.Second)) {
		t.Fatal("exit missed")
	}
	if d.Observe(uuSnapshot{}, n.Add(30*time.Second)) {
		t.Fatal("duplicate action")
	}
	d.Observe(uuSnapshot{Running: true}, n.Add(31*time.Second))
	d.Observe(uuSnapshot{}, n.Add(32*time.Second))
	if !d.Observe(uuSnapshot{}, n.Add(38*time.Second)) {
		t.Fatal("second exit missed")
	}
}
func TestRapidRestartAndUpgradeDoNotTrigger(t *testing.T) {
	var d exitDetector
	n := time.Now()
	d.Observe(uuSnapshot{Running: true}, n)
	d.Observe(uuSnapshot{}, n.Add(time.Second))
	d.Observe(uuSnapshot{Running: true}, n.Add(3*time.Second))
	if d.Observe(uuSnapshot{}, n.Add(9*time.Second)) {
		t.Fatal("restart triggered")
	}
	if d.Observe(uuSnapshot{Updating: true}, n.Add(50*time.Second)) {
		t.Fatal("upgrade triggered")
	}
	if d.Observe(uuSnapshot{}, n.Add(51*time.Second)) {
		t.Fatal("upgrade delay not reset")
	}
}

func TestEvaluateUUCandidatesSkipsTransientPerPIDErrors(t *testing.T) {
	const root = `c:\program files\netease\gameviewer\`
	entries := []processCandidate{
		{PID: 1001, Name: "gameviewer.exe"},
		{PID: 1002, Name: "upgrade.exe"},
		{PID: 1003, Name: "gameviewer.exe"},
	}
	snap, handles, err := evaluateUUCandidates(root, entries, false, func(pid uint32, _ bool) (string, windows.Handle, error) {
		switch pid {
		case 1001:
			return "", 0, windows.ERROR_INVALID_PARAMETER
		case 1002:
			return "", 0, windows.ERROR_ACCESS_DENIED
		case 1003:
			return root + "gameviewer.exe", 0, nil
		default:
			return "", 0, errors.New("unexpected pid")
		}
	})
	if err != nil {
		t.Fatalf("transient per-PID errors should not abort scan: %v", err)
	}
	if !snap.Running || snap.Updating || len(handles) != 0 {
		t.Fatalf("unexpected snapshot/handles: %+v handles=%d", snap, len(handles))
	}

	if _, _, err := evaluateUUCandidates(root, entries[:1], false, func(uint32, bool) (string, windows.Handle, error) {
		return "", 0, windows.ERROR_NOT_ENOUGH_MEMORY
	}); err == nil {
		t.Fatal("non-transient error should still fail scan")
	}
}

func TestIsValidTestNamespace(t *testing.T) {
	for _, valid := range []string{"a", "validate-123", "NS_01.test-run", strings.Repeat("a", 32)} {
		if !isValidTestNamespace(valid) {
			t.Errorf("expected valid namespace %q", valid)
		}
	}
	for _, invalid := range []string{"", strings.Repeat("a", 33), "bad/ns", `bad\ns`, "has space", "中文"} {
		if isValidTestNamespace(invalid) {
			t.Errorf("expected invalid namespace %q", invalid)
		}
	}
}

func TestIsTaskNotFoundError(t *testing.T) {
	exitErr := &exec.ExitError{}
	if !isTaskNotFoundError(exitErr, []byte("ERROR: The system cannot find the file specified.\r\n")) {
		t.Fatal("expected English missing-task message to be recognized")
	}
	if !isTaskNotFoundError(exitErr, []byte("错误: 系统找不到指定的文件。\r\n")) {
		t.Fatal("expected Chinese missing-task message to be recognized")
	}
	if isTaskNotFoundError(exitErr, []byte("ERROR: Access is denied.\r\n")) {
		t.Fatal("access denied must not be masked as missing task")
	}
	if isTaskNotFoundError(errors.New("exec missing"), []byte("cannot find the file specified")) {
		t.Fatal("non-ExitError launch failure must not be masked as missing task")
	}
}

func TestCanConfirmNoMonitorRequiresAbsentMutexAndHeartbeats(t *testing.T) {
	active := &heartbeat{PID: 42, Status: "waiting"}
	stopped := &heartbeat{PID: 42, Status: "stopped"}
	cases := []struct {
		name        string
		mutexExists bool
		active      *heartbeat
		stopped     *heartbeat
		want        bool
	}{
		{name: "no monitor", want: true},
		{name: "mutex exists without heartbeat", mutexExists: true, want: false},
		{name: "active heartbeat without mutex", active: active, want: false},
		{name: "stopped heartbeat without mutex", stopped: stopped, want: false},
		{name: "mutex and active heartbeat", mutexExists: true, active: active, want: false},
		{name: "mutex and stopped heartbeat", mutexExists: true, stopped: stopped, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canConfirmNoMonitor(tc.mutexExists, tc.active, tc.stopped); got != tc.want {
				t.Fatalf("canConfirmNoMonitor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAutoExitPreferenceDecision(t *testing.T) {
	cases := []struct {
		name             string
		preferences      autoPreferences
		disableAttempted bool
		disableErr       error
		want             bool
	}{
		{name: "disable only", preferences: autoPreferences{AutoDisable: true}, disableAttempted: true, want: false},
		{name: "exit only", preferences: autoPreferences{AutoExit: true}, want: true},
		{name: "both with successful disable", preferences: autoPreferences{AutoDisable: true, AutoExit: true}, disableAttempted: true, want: true},
		{name: "both when disable failed", preferences: autoPreferences{AutoDisable: true, AutoExit: true}, disableAttempted: true, disableErr: errors.New("disable failed"), want: false},
		{name: "disable became requested but was not attempted", preferences: autoPreferences{AutoDisable: true, AutoExit: true}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoExitAllowed(tc.preferences, tc.disableAttempted, tc.disableErr); got != tc.want {
				t.Fatalf("autoExitAllowed() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompletedUUExitHeartbeatRequiresCompletionTimestamp(t *testing.T) {
	h := heartbeat{
		PID: 42, Started: 123456, Token: "0123456789abcdef0123456789abcdef",
		Status: "stopped", UUExitAt: 1781548800123,
	}
	if got := completedUUExitAt(h); got != h.UUExitAt {
		t.Fatalf("completed heartbeat timestamp = %d, want %d", got, h.UUExitAt)
	}

	h.UUExitAt = 0 // A manual monitor stop has no UU-exit timestamp.
	if got := completedUUExitAt(h); got != 0 {
		t.Fatalf("manual stop reported UU exit at %d", got)
	}

	h.UUExitAt = 1781548800123
	h.Status = "armed"
	if got := completedUUExitAt(h); got != 0 {
		t.Fatalf("active monitor reported UU exit at %d", got)
	}
}

func TestUUExitCompletionRequiresKnownAbsentUU(t *testing.T) {
	const completionAt int64 = 1781548800123
	cases := []struct {
		name    string
		state   uuSnapshot
		scanErr error
		want    int64
	}{
		{name: "UU absent", want: completionAt},
		{name: "UU running", state: uuSnapshot{Running: true}, want: 0},
		{name: "UU updating", state: uuSnapshot{Updating: true}, want: 0},
		{name: "scan failed", scanErr: errors.New("process scan failed"), want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := completionForUUState(tc.state, tc.scanErr, completionAt); got != tc.want {
				t.Fatalf("completionForUUState() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestStopWatchWithInProcessElevatedTrayMonitor(t *testing.T) {
	const hostPID = uint32(99991)
	id := monitorIdentity{PID: hostPID, Started: 123456789, Token: "0123456789abcdef0123456789abcdef"}
	activeHB := heartbeat{PID: id.PID, Started: id.Started, Token: id.Token, Status: "armed"}
	stoppedHB := heartbeat{PID: id.PID, Started: id.Started, Token: id.Token, Status: "stopped"}

	stopped := false
	closed := false
	start := time.Now()
	err := stopWatchWith(stopWatchDeps{
		stopInProcess: func() {},
		readHB: func() *heartbeat {
			if stopped {
				return &stoppedHB
			}
			return &activeHB
		},
		mutexExists: func() (bool, error) {
			return !stopped, nil
		},
		openProc: func(got monitorIdentity) (windows.Handle, bool, error) {
			if got != id {
				t.Fatalf("unexpected identity: %+v", got)
			}
			return windows.Handle(0x1234), true, nil
		},
		signalStop: func() {
			stopped = true
		},
		waitProc: func(_ windows.Handle, _ uint32) (uint32, error) {
			// Simulate elevated --tray host staying alive after monitor stops.
			return uint32(windows.WAIT_TIMEOUT), nil
		},
		closeProc: func(_ windows.Handle) error {
			closed = true
			return nil
		},
		selfPID: 11111,
		timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("stopWatchWith failed against live host --tray process: %v", err)
	}
	if !stopped || !closed {
		t.Fatalf("expected signalStop and closeProc to run: stopped=%v closed=%v", stopped, closed)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stopWatchWith stalled for %s instead of confirming mutex release", elapsed)
	}
}

func TestBuildCrossIntegritySACoversEventAndMutexAccess(t *testing.T) {
	for _, access := range []uint32{eventAllAccess, mutexAllAccess} {
		sa, err := buildCrossIntegritySA(access)
		if err != nil || sa == nil || sa.SecurityDescriptor == nil {
			t.Fatalf("buildCrossIntegritySA(0x%x) failed: sa=%v err=%v", access, sa, err)
		}
	}
}
