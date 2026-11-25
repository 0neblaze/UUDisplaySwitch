package main

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func isValidTestNamespace(ns string) bool {
	if len(ns) == 0 || len(ns) > 32 {
		return false
	}
	for i := 0; i < len(ns); i++ {
		c := ns[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// kernelObjectName returns the session-local kernel object name, allowing
// isolated --read-only verification tests via UUDISPLAYSWITCH_TEST_NS without
// touching a live desktop tray/monitor instance.
func kernelObjectName(base string) string {
	if ns := os.Getenv("UUDISPLAYSWITCH_TEST_NS"); isValidTestNamespace(ns) {
		return "Local\\UUDisplaySwitch.Test." + ns + "." + base
	}
	return "Local\\UUDisplaySwitch." + base
}

func windowClassForNamespace(base string) string {
	if ns := os.Getenv("UUDISPLAYSWITCH_TEST_NS"); isValidTestNamespace(ns) {
		return base + ".Test." + ns
	}
	return base
}

const (
	eventModifyState = 0x0002
	eventAllAccess   = 0x1F0003
	mutexAllAccess   = 0x1F0001
)

func currentUserSIDString() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

// buildCrossIntegritySA creates SECURITY_ATTRIBUTES granting the requested access
// to Builtin Administrators, LocalSystem, and the interactive user's SID so an
// elevated --watch process and an unelevated --tray process can open/signal the
// same named kernel object without ERROR_ACCESS_DENIED.
func buildCrossIntegritySA(access uint32) (*windows.SecurityAttributes, error) {
	sid, err := currentUserSIDString()
	if err != nil {
		return nil, err
	}
	sddl := fmt.Sprintf("D:(A;;0x%x;;;BA)(A;;0x%x;;;SY)(A;;0x%x;;;%s)", access, access, access, sid)
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}, nil
}

func createOrOpenSharedEvent(baseName string, manualReset bool) (windows.Handle, error) {
	fullName := kernelObjectName(baseName)
	namePtr, err := windows.UTF16PtrFromString(fullName)
	if err != nil {
		return 0, err
	}
	sa, saErr := buildCrossIntegritySA(eventAllAccess)
	var saPtr uintptr
	if saErr == nil && sa != nil {
		saPtr = uintptr(unsafe.Pointer(sa))
	}
	manual := uintptr(0)
	if manualReset {
		manual = 1
	}
	h, _, callErr := procCreateEventW.Call(saPtr, manual, 0, uintptr(unsafe.Pointer(namePtr)))
	runtime.KeepAlive(namePtr)
	runtime.KeepAlive(sa)
	if h != 0 {
		return windows.Handle(h), nil
	}
	// If already created by an elevated peer, open with SYNCHRONIZE | EVENT_MODIFY_STATE.
	opened, openErr := windows.OpenEvent(windows.SYNCHRONIZE|eventModifyState, false, namePtr)
	if openErr == nil {
		return opened, nil
	}
	return 0, win32CallError("CreateEventW("+fullName+")", callErr)
}

func pulseNamedEvent(baseName string) {
	fullName := kernelObjectName(baseName)
	namePtr, err := windows.UTF16PtrFromString(fullName)
	if err != nil {
		return
	}
	h, err := windows.OpenEvent(eventModifyState, false, namePtr)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	procSetEvent.Call(uintptr(h))
}

func signalMonitorStop() {
	pulseNamedEvent("MonitorStop")
}

func signalMonitorWake() {
	pulseNamedEvent("MonitorWake")
}

func signalMonitorStateChanged() {
	pulseNamedEvent("MonitorStateChanged")
}
