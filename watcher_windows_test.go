package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestMonitorStopRequestIsBoundToFreshMonitorIdentity(t *testing.T) {
	now := time.Now()
	identity := monitorIdentity{PID: 42, Started: 123456, Token: "0123456789abcdef0123456789abcdef"}
	request := monitorStopRequest{
		PID: identity.PID, Started: identity.Started, MonitorToken: identity.Token,
		RequestedAt: now.UnixNano(), RequestToken: "abcdef0123456789abcdef0123456789",
	}
	if !request.matches(identity, now) {
		t.Fatal("fresh matching request was rejected")
	}
	if request.matches(monitorIdentity{PID: identity.PID + 1, Started: identity.Started, Token: identity.Token}, now) {
		t.Fatal("request matched another PID")
	}
	if request.matches(monitorIdentity{PID: identity.PID, Started: identity.Started + 1, Token: identity.Token}, now) {
		t.Fatal("request matched a reused PID")
	}
	if request.matches(monitorIdentity{PID: identity.PID, Started: identity.Started, Token: "fedcba9876543210fedcba9876543210"}, now) {
		t.Fatal("request matched another monitor instance")
	}
	if request.matches(identity, now.Add(monitorStopRequestMaxAge+time.Nanosecond)) {
		t.Fatal("expired request was accepted")
	}
	if request.matches(identity, now.Add(-time.Second)) {
		t.Fatal("future-dated request was accepted")
	}
}

func TestMonitorStopAcknowledgementIsBoundToRequest(t *testing.T) {
	request := monitorStopRequest{
		PID: 42, Started: 123456, MonitorToken: "0123456789abcdef0123456789abcdef",
		RequestedAt: time.Now().UnixNano(), RequestToken: "abcdef0123456789abcdef0123456789",
	}
	ack := monitorStopAck{
		PID: request.PID, Started: request.Started, MonitorToken: request.MonitorToken,
		RequestToken: request.RequestToken, StoppedAt: time.Now().UnixNano(),
	}
	if !ack.matches(request) {
		t.Fatal("matching acknowledgement was rejected")
	}
	ack.RequestToken = "fedcba9876543210fedcba9876543210"
	if ack.matches(request) {
		t.Fatal("acknowledgement for another request was accepted")
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
