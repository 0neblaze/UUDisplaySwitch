package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var shell32 = windows.NewLazySystemDLL("shell32.dll")
var shellExecuteEx = shell32.NewProc("ShellExecuteExW")
var isUserAdmin = shell32.NewProc("IsUserAnAdmin")

// Layout follows SHELLEXECUTEINFOW; uintptr fields retain native alignment.
type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	Window     uintptr
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   uintptr
	IDList     uintptr
	Class      *uint16
	ClassKey   uintptr
	HotKey     uint32
	Icon       uintptr
	Process    windows.Handle
}

func elevated() bool { r, _, _ := isUserAdmin.Call(); return r != 0 }

func launchElevated(parameters string, owner uintptr) (windows.Handle, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString(parameters)
	info := shellExecuteInfo{
		Mask:       0x40 | 0x100 | 0x400,
		Window:     owner,
		Verb:       verb,
		File:       file,
		Parameters: args,
		Show:       0,
		Size:       uint32(unsafe.Sizeof(shellExecuteInfo{})),
	}
	r, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(verb)
	runtime.KeepAlive(file)
	runtime.KeepAlive(args)
	runtime.KeepAlive(&info)
	if r == 0 {
		return 0, callErr
	}
	return info.Process, nil
}

func performChange(mode string, owner uintptr) error {
	if mode != "enable" && mode != "disable" {
		return errors.New("无效的操作。")
	}
	if elevated() {
		return runHelper(mode)
	}
	proc, err := launchElevated("--apply "+mode, owner)
	if err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("已取消管理员授权，设备未切换。")
		}
		return fmt.Errorf("无法请求管理员权限：%w", err)
	}
	if proc == 0 {
		return errors.New("无法取得切换进程，未确认结果。请刷新设备状态。")
	}
	defer windows.CloseHandle(proc)
	wait, err := windows.WaitForSingleObject(proc, 60000)
	if err != nil {
		return err
	}
	if wait == uint32(windows.WAIT_TIMEOUT) {
		return errors.New("设备切换进程仍未结束，请稍后刷新状态。为避免冲突，暂勿重复切换。")
	}
	var code uint32
	if err = windows.GetExitCodeProcess(proc, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("切换未完成（进程代码 %d）。请展开操作记录查看原因，或刷新状态。", code)
	}
	state, err := (nativeDevice{}).Read()
	if err != nil {
		return err
	}
	expected := uint32(0)
	if mode == "disable" {
		expected = 22
	}
	if state.Problem != expected {
		return errors.New("切换进程已结束，但设备未达到预期状态。请查看操作记录。")
	}
	return nil
}

func runHelper(mode string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !elevated() {
		return errors.New("切换设备需要管理员权限。")
	}
	name, _ := windows.UTF16PtrFromString(`Local\UUDisplaySwitch.DeviceMutation`)
	mutex, err := windows.CreateMutex(nil, true, name)
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return errors.New("另一次设备切换尚未结束，请稍后再试。")
	}
	if err != nil {
		return err
	}
	defer windows.ReleaseMutex(mutex)
	result, err := applyNative(mode)
	record := activity{Time: time.Now().Format(time.RFC3339), Mode: mode, OK: err == nil, Problem: result.Problem}
	if err != nil {
		record.Message = err.Error()
	} else if mode == "enable" {
		record.Message = "虚拟适配器已开启；可在 UU 中开启黑屏功能。"
	} else {
		record.Message = "虚拟适配器已关闭；请重新启动游戏。"
	}
	if logErr := appendActivity(record); logErr != nil {
		if err != nil {
			return fmt.Errorf("%v；操作记录写入失败：%v", err, logErr)
		}
		return fmt.Errorf("设备已切换，但操作记录保存失败：%w", logErr)
	}
	return err
}
