package main

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const deviceID = `ROOT\DISPLAY\0000`
const deviceName = "GameViewer Virtual Display Adapter"
const displayClass = "{4d36e968-e325-11ce-bfc1-08002be10318}"

var (
	cfgmgr     = windows.NewLazySystemDLL("cfgmgr32.dll")
	cmLocate   = cfgmgr.NewProc("CM_Locate_DevNodeW")
	cmProperty = cfgmgr.NewProc("CM_Get_DevNode_Registry_PropertyW")
	cmEnable   = cfgmgr.NewProc("CM_Enable_DevNode")
	cmDisable  = cfgmgr.NewProc("CM_Disable_DevNode")
)

type deviceSnapshot struct {
	Name       string `json:"name"`
	InstanceID string `json:"instanceId"`
	Class      string `json:"class"`
	Problem    uint32 `json:"problem"`
	Flags      uint32 `json:"flags"`
}

type nativeDevice struct{}

func locate() (windows.DEVINST, error) {
	var node windows.DEVINST
	id, _ := windows.UTF16PtrFromString(deviceID)
	ret, _, _ := cmLocate.Call(uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(id)), 0)
	if ret != 0 {
		return 0, fmt.Errorf("未找到指定的 UU 虚拟显示适配器（CM 0x%X）。请检查 UU 是否更新或重装。", ret)
	}
	return node, nil
}

func property(node windows.DEVINST, key uint32) (string, uintptr) {
	var valueType uint32
	buf := make([]uint16, 1024)
	size := uint32(len(buf) * 2)
	ret, _, _ := cmProperty.Call(uintptr(node), uintptr(key), uintptr(unsafe.Pointer(&valueType)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if ret != 0 {
		return "", ret
	}
	if valueType != windows.REG_SZ {
		return "", 0x1F
	}
	return windows.UTF16ToString(buf), 0
}

func (nativeDevice) Read() (deviceSnapshot, error) {
	s := deviceSnapshot{InstanceID: deviceID}
	node, err := locate()
	if err != nil {
		return s, err
	}
	name, ret := property(node, 0x0D) // FriendlyName is optional; DeviceDesc is the standard fallback.
	if ret == 0x25 || ret == 0x0D {
		name, ret = property(node, 0x01)
	}
	if ret != 0 {
		return s, fmt.Errorf("无法读取设备名称（CM 0x%X）。", ret)
	}
	class, ret := property(node, 0x09)
	if ret != 0 {
		return s, fmt.Errorf("无法读取设备类别（CM 0x%X）。", ret)
	}
	s.Name, s.Class = name, class
	if err = validateIdentity(s); err != nil {
		return s, err
	}
	if err = windows.CM_Get_DevNode_Status(&s.Flags, &s.Problem, node, 0); err != nil {
		return s, fmt.Errorf("读取设备状态失败：%w", err)
	}
	return s, nil
}

func validateIdentity(s deviceSnapshot) error {
	if s.InstanceID != deviceID || s.Name != deviceName || !strings.EqualFold(s.Class, displayClass) {
		return fmt.Errorf("设备身份不匹配，已停止操作。只允许切换指定的 UU 虚拟显示适配器。")
	}
	return nil
}

func (nativeDevice) Set(enabled bool) error {
	node, err := locate()
	if err != nil {
		return err
	}
	var ret uintptr
	if enabled {
		ret, _, _ = cmEnable.Call(uintptr(node), 0)
	} else {
		// No force-disable: ask the driver; persist the user's choice through reboot.
		ret, _, _ = cmDisable.Call(uintptr(node), 0x04|0x08)
	}
	if ret != 0 {
		return fmt.Errorf("Windows 未能完成设备切换（CM 0x%X）。设备可能正在使用，详情见操作记录。", ret)
	}
	return nil
}

type deviceBackend interface {
	Read() (deviceSnapshot, error)
	Set(bool) error
}

func changeDevice(backend deviceBackend, mode string, attempts int, pause func()) (deviceSnapshot, error) {
	if mode != "enable" && mode != "disable" {
		return deviceSnapshot{}, fmt.Errorf("无效的切换操作。")
	}
	before, err := backend.Read()
	if err != nil {
		return before, err
	}
	if err = validateIdentity(before); err != nil {
		return before, err
	}
	if before.Problem != 0 && before.Problem != 22 {
		return before, fmt.Errorf("设备存在其他故障（代码 %d），未执行切换。", before.Problem)
	}
	expected := uint32(0)
	if mode == "disable" {
		expected = 22
	}
	if before.Problem == expected {
		return before, nil
	}
	if err = backend.Set(mode == "enable"); err != nil {
		return before, err
	}
	var after deviceSnapshot
	for i := 0; i < attempts; i++ {
		after, err = backend.Read()
		if err == nil {
			if err = validateIdentity(after); err != nil {
				return after, err
			}
			if after.Problem == expected {
				return after, nil
			}
		}
		pause()
	}
	return after, fmt.Errorf("切换请求已提交，但 Windows 尚未确认目标状态。请刷新检查；系统可能需要重启。")
}

func applyNative(mode string) (deviceSnapshot, error) {
	return changeDevice(nativeDevice{}, mode, 24, func() { time.Sleep(250 * time.Millisecond) })
}
