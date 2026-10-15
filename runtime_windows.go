package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getCurrentPackageFullName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentPackageFullName")

func currentPackageName() (string, error) {
	var size uint32
	result, _, _ := getCurrentPackageFullName.Call(uintptr(unsafe.Pointer(&size)), 0)
	if result == 15700 { // APPMODEL_ERROR_NO_PACKAGE
		return "", nil
	}
	if result != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return "", fmt.Errorf("Could not identify application package: Windows error %d", result)
	}
	buffer := make([]uint16, size)
	result, _, _ = getCurrentPackageFullName.Call(uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buffer[0])))
	if result != 0 {
		return "", fmt.Errorf("Could not read application package: Windows error %d", result)
	}
	return windows.UTF16ToString(buffer), nil
}

func resolvedFilePath(file *os.File) (string, error) {
	buffer := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if n >= uint32(len(buffer)) {
		return "", fmt.Errorf("Resolved data path is too long")
	}
	path := windows.UTF16ToString(buffer[:n])
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`), nil
	}
	return strings.TrimPrefix(path, `\\?\`), nil
}

func sameStoragePath(expected, actual string) bool {
	return strings.EqualFold(filepath.Clean(expected), filepath.Clean(actual))
}

func effectiveMonitorPath() (string, error) {
	file, err := os.Open(filepath.Join(dataDirectory(), "monitor.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	return resolvedFilePath(file)
}

// A process can inherit an MSIX file view without GetCurrentPackageFullName
// reporting an identity. Verify the file handle, not only the package API.
func requireDesktopRuntime(probeWrite bool) error {
	var file *os.File
	var err error
	if probeWrite {
		if err = os.MkdirAll(dataDirectory(), 0700); err != nil {
			return err
		}
		file, err = os.CreateTemp(dataDirectory(), ".storage-probe-*")
	} else {
		file, err = os.Open(filepath.Join(dataDirectory(), "monitor.json"))
		if os.IsNotExist(err) {
			return nil
		}
	}
	if err != nil {
		return err
	}
	expected := file.Name()
	actual, err := resolvedFilePath(file)
	closeErr := file.Close()
	if probeWrite {
		if removeErr := os.Remove(expected); removeErr != nil {
			return removeErr
		}
	}
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if !sameStoragePath(expected, actual) {
		return fmt.Errorf("Application data was redirected from %s to %s. Start UU Display from the Windows desktop; monitoring was not started in this environment", expected, actual)
	}
	return nil
}
