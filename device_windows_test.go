package main

import (
	"errors"
	"testing"
)

type fakeDevice struct {
	state   deviceSnapshot
	setErr  error
	readErr error
	calls   int
	stuck   bool
}

func (f *fakeDevice) Read() (deviceSnapshot, error) { return f.state, f.readErr }
func (f *fakeDevice) Set(on bool) error {
	f.calls++
	if f.setErr != nil {
		return f.setErr
	}
	if !f.stuck {
		f.state.Problem = 22
		if on {
			f.state.Problem = 0
		}
	}
	return nil
}
func fixture(problem uint32) *fakeDevice {
	return &fakeDevice{state: deviceSnapshot{Name: deviceName, InstanceID: deviceID, Class: displayClass, Problem: problem}}
}
func TestChangeRoundTrip(t *testing.T) {
	f := fixture(22)
	s, e := changeDevice(f, "enable", 2, func() {})
	if e != nil || s.Problem != 0 || f.calls != 1 {
		t.Fatalf("enable: %+v %v", s, e)
	}
	s, e = changeDevice(f, "disable", 2, func() {})
	if e != nil || s.Problem != 22 || f.calls != 2 {
		t.Fatalf("disable: %+v %v", s, e)
	}
}
func TestAlreadySelectedDoesNotMutate(t *testing.T) {
	for _, mode := range []string{"enable", "disable"} {
		p := uint32(0)
		if mode == "disable" {
			p = 22
		}
		f := fixture(p)
		_, e := changeDevice(f, mode, 2, func() {})
		if e != nil || f.calls != 0 {
			t.Fatal("idempotent change mutated", e)
		}
	}
}
func TestRejectUnexpectedDevice(t *testing.T) {
	for _, field := range []string{"name", "id", "class"} {
		f := fixture(22)
		switch field {
		case "name":
			f.state.Name = "NVIDIA GeForce RTX 4070 Laptop GPU"
		case "id":
			f.state.InstanceID = "PCI\\OTHER"
		case "class":
			f.state.Class = "OTHER"
		}
		_, e := changeDevice(f, "enable", 2, func() {})
		if e == nil || f.calls != 0 {
			t.Fatal("unexpected identity was accepted", field)
		}
	}
}
func TestRejectOtherDeviceProblem(t *testing.T) {
	f := fixture(43)
	_, e := changeDevice(f, "enable", 2, func() {})
	if e == nil || f.calls != 0 {
		t.Fatal("faulting device was mutated")
	}
}
func TestFailedCommandCannotReportSuccess(t *testing.T) {
	f := fixture(22)
	f.setErr = errors.New("access denied")
	_, e := changeDevice(f, "enable", 2, func() {})
	if e == nil || f.state.Problem != 22 {
		t.Fatal("failed command reported success")
	}
}
func TestVerifyRealStateAfterCommand(t *testing.T) {
	f := fixture(22)
	f.stuck = true
	_, e := changeDevice(f, "enable", 2, func() {})
	if e == nil {
		t.Fatal("unconfirmed state reported success")
	}
}
func TestInvalidModeAndReadFailureNeverMutate(t *testing.T) {
	f := fixture(22)
	if _, e := changeDevice(f, "toggle-anything", 2, func() {}); e == nil || f.calls != 0 {
		t.Fatal("invalid mode accepted")
	}
	f.readErr = errors.New("missing device")
	if _, e := changeDevice(f, "enable", 2, func() {}); e == nil || f.calls != 0 {
		t.Fatal("read failure ignored")
	}
}
