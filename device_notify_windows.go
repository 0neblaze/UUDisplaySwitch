package main

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	cmRegisterNotification   = cfgmgr.NewProc("CM_Register_Notification")
	cmUnregisterNotification = cfgmgr.NewProc("CM_Unregister_Notification")
	deviceNotifyCallbackPtr  = windows.NewCallback(cmDeviceNotifyCallback)
	deviceNotifyMu           sync.Mutex
	deviceNotifyWakeEvt      windows.Handle
	deviceNotifyPending      *atomic.Bool
)

const (
	cmNotifyFilterTypeDeviceInstance = 2
	maxDeviceIDLen                   = 200
)

// cmNotifyFilter matches CM_NOTIFY_FILTER for CM_NOTIFY_FILTER_TYPE_DEVICEINSTANCE on Windows x64:
// cbSize (4) + Flags (4) + FilterType (4) + Reserved (4) + InstanceId[200] (400) = 416 bytes.
type cmNotifyFilter struct {
	_          [0]uintptr
	cbSize     uint32
	flags      uint32
	filterType uint32
	reserved   uint32
	instanceID [maxDeviceIDLen]uint16
}

func cmDeviceNotifyCallback(hNotify, context uintptr, action uint32, eventData uintptr, eventDataSize uint32) uintptr {
	deviceNotifyMu.Lock()
	wakeEvt := deviceNotifyWakeEvt
	pending := deviceNotifyPending
	deviceNotifyMu.Unlock()
	if pending != nil {
		pending.Store(true)
	}
	if wakeEvt != 0 {
		_ = windows.SetEvent(wakeEvt)
	}
	return 0
}

type deviceNotificationSubscription struct {
	context uintptr
}

func subscribeDeviceNotifications(wakeEvt windows.Handle, pending *atomic.Bool) *deviceNotificationSubscription {
	if wakeEvt == 0 {
		return nil
	}
	if err := cmRegisterNotification.Find(); err != nil {
		return nil
	}
	var filter cmNotifyFilter
	filter.cbSize = uint32(unsafe.Sizeof(filter))
	filter.filterType = cmNotifyFilterTypeDeviceInstance
	copy(filter.instanceID[:], toUTF16Array(deviceID, len(filter.instanceID)))

	deviceNotifyMu.Lock()
	deviceNotifyWakeEvt = wakeEvt
	deviceNotifyPending = pending
	deviceNotifyMu.Unlock()

	var handle uintptr
	ret, _, _ := cmRegisterNotification.Call(
		uintptr(unsafe.Pointer(&filter)),
		0,
		deviceNotifyCallbackPtr,
		uintptr(unsafe.Pointer(&handle)),
	)
	if ret != 0 || handle == 0 {
		return nil
	}
	return &deviceNotificationSubscription{context: handle}
}

func (s *deviceNotificationSubscription) Close() {
	if s == nil || s.context == 0 {
		return
	}
	deviceNotifyMu.Lock()
	deviceNotifyWakeEvt = 0
	deviceNotifyPending = nil
	deviceNotifyMu.Unlock()
	if cmUnregisterNotification.Find() == nil {
		cmUnregisterNotification.Call(s.context)
	}
	s.context = 0
}
