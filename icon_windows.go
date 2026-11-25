package main

import (
	"encoding/binary"
	"math"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")

	procCreateIconFromResourceEx = user32Tray.NewProc("CreateIconFromResourceEx")
	procCreateIconIndirect       = user32Tray.NewProc("CreateIconIndirect")
	procDrawIconEx               = user32Tray.NewProc("DrawIconEx")
	procGetSystemMetricsForDpi   = user32Tray.NewProc("GetSystemMetricsForDpi")
	procGetDpiForSystem          = user32Tray.NewProc("GetDpiForSystem")
	procGetDpiForWindow          = user32Tray.NewProc("GetDpiForWindow")
	procGetDC                    = user32Tray.NewProc("GetDC")
	procReleaseDC                = user32Tray.NewProc("ReleaseDC")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap       = gdi32.NewProc("CreateBitmap")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
)

const (
	smCxSmIcon = 49
	smCxIcon   = 11
	diNormal   = 0x0003
)

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

func systemDPI() uint32 {
	if procGetDpiForSystem.Find() == nil {
		if dpi, _, _ := procGetDpiForSystem.Call(); dpi != 0 {
			return uint32(dpi)
		}
	}
	return 96
}

func windowDPI(hwnd uintptr) uint32 {
	if hwnd != 0 && procGetDpiForWindow.Find() == nil {
		if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi != 0 {
			return uint32(dpi)
		}
	}
	return systemDPI()
}

func smallIconSizeForDPI(dpi uint32) int32 {
	if procGetSystemMetricsForDpi.Find() == nil {
		if v, _, _ := procGetSystemMetricsForDpi.Call(smCxSmIcon, uintptr(dpi)); v > 0 {
			return int32(v)
		}
	}
	return int32((16*dpi + 48) / 96)
}

func largeIconSizeForDPI(dpi uint32) int32 {
	if procGetSystemMetricsForDpi.Find() == nil {
		if v, _, _ := procGetSystemMetricsForDpi.Call(smCxIcon, uintptr(dpi)); v > 0 {
			return int32(v)
		}
	}
	return int32((32*dpi + 48) / 96)
}

func loadEmbeddedIconAtSize(targetSize int32) uintptr {
	ico := embeddedAppIcon
	if len(ico) >= 6 {
		count := int(binary.LittleEndian.Uint16(ico[4:6]))
		if len(ico) >= 6+count*16 && count > 0 {
			bestIdx := -1
			bestSize := int32(0)
			for i := 0; i < count; i++ {
				entry := ico[6+i*16 : 6+(i+1)*16]
				w := int32(entry[0])
				if w == 0 {
					w = 256
				}
				if bestIdx == -1 {
					bestIdx, bestSize = i, w
					continue
				}
				if bestSize < targetSize && w > bestSize {
					bestIdx, bestSize = i, w
				} else if bestSize >= targetSize && w >= targetSize && w < bestSize {
					bestIdx, bestSize = i, w
				}
			}
			if bestIdx >= 0 {
				entry := ico[6+bestIdx*16 : 6+(bestIdx+1)*16]
				resBytes := binary.LittleEndian.Uint32(entry[8:12])
				resOff := binary.LittleEndian.Uint32(entry[12:16])
				if uint64(resOff)+uint64(resBytes) <= uint64(len(ico)) && resBytes > 0 {
					slice := ico[resOff : resOff+resBytes]
					hIcon, _, _ := procCreateIconFromResourceEx.Call(
						uintptr(unsafe.Pointer(&slice[0])),
						uintptr(resBytes),
						1,
						0x00030000,
						uintptr(targetSize),
						uintptr(targetSize),
						0,
					)
					runtime.KeepAlive(slice)
					if hIcon != 0 {
						return hIcon
					}
				}
			}
		}
	}
	// Fallback to embedded PE resource #1.
	if mod, _, _ := procGetModuleHandleW.Call(0); mod != 0 {
		if hIcon, _, _ := procLoadImageW.Call(mod, 1, imageIcon, uintptr(targetSize), uintptr(targetSize), 0); hIcon != 0 {
			return hIcon
		}
	}
	return 0
}

// buildBadgedTrayIcon composites a crisp 4x-supersampled status dot onto baseIcon
// so the user can tell Enabled (green), Disabled (slate gray), or Busy/Error at a glance.
func buildBadgedTrayIcon(baseIcon uintptr, size int32, r, g, b byte) uintptr {
	if baseIcon == 0 || size <= 0 {
		return 0
	}
	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return 0
	}
	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	procReleaseDC.Call(0, screenDC)
	if memDC == 0 {
		return 0
	}
	defer procDeleteDC.Call(memDC)

	bmi := bitmapInfoHeader{
		biSize:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:    size,
		biHeight:   -size, // top-down
		biPlanes:   1,
		biBitCount: 32,
	}
	var bitsPtr unsafe.Pointer
	hbmColor, _, _ := procCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bitsPtr)), 0, 0)
	runtime.KeepAlive(&bmi)
	if hbmColor == 0 || bitsPtr == nil {
		return 0
	}
	defer procDeleteObject.Call(hbmColor)

	oldBmp, _, _ := procSelectObject.Call(memDC, hbmColor)
	procDrawIconEx.Call(memDC, 0, 0, baseIcon, uintptr(size), uintptr(size), 0, 0, diNormal)
	if oldBmp != 0 {
		procSelectObject.Call(memDC, oldBmp)
	}

	pixels := unsafe.Slice((*uint32)(bitsPtr), int(size*size))
	badgeOuterR := math.Max(3.2, float64(size)*0.22)
	borderW := math.Max(1.1, float64(size)*0.065)
	badgeInnerR := badgeOuterR - borderW
	cx := float64(size) - badgeOuterR - 0.5
	cy := float64(size) - badgeOuterR - 0.5

	for py := int32(0); py < size; py++ {
		for px := int32(0); px < size; px++ {
			// Quick bounding box filter
			if float64(px) < cx-badgeOuterR-1.5 || float64(py) < cy-badgeOuterR-1.5 {
				continue
			}
			// 4x4 supersampling for smooth anti-aliased circle edge
			var covOuter, covInner float64
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					fx := float64(px) + (float64(sx)+0.5)*0.25 - cx
					fy := float64(py) + (float64(sy)+0.5)*0.25 - cy
					d := math.Hypot(fx, fy)
					if d <= badgeOuterR {
						covOuter += 1.0 / 16.0
					}
					if d <= badgeInnerR {
						covInner += 1.0 / 16.0
					}
				}
			}
			if covOuter <= 0 {
				continue
			}
			// Outer ring: dark charcoal (24,24,27,245); inner fill: (r,g,b,255)
			ringFrac := covOuter - covInner
			srcA := ringFrac*(245.0/255.0) + covInner*1.0
			srcR := (ringFrac*24.0 + covInner*float64(r)) / math.Max(covOuter, 1e-6)
			srcG := (ringFrac*24.0 + covInner*float64(g)) / math.Max(covOuter, 1e-6)
			srcB := (ringFrac*27.0 + covInner*float64(b)) / math.Max(covOuter, 1e-6)

			idx := int(py*size + px)
			dst := pixels[idx]
			dstB := float64(dst & 0xFF)
			dstG := float64((dst >> 8) & 0xFF)
			dstR := float64((dst >> 16) & 0xFF)
			dstA := float64((dst>>24)&0xFF) / 255.0

			outA := srcA + dstA*(1.0-srcA)
			var outR, outG, outB float64
			if outA > 1e-6 {
				// Premultiplied-safe composite stored back as straight/ARGB for CreateIconIndirect
				outR = (srcR*srcA + dstR*dstA*(1.0-srcA)) / outA
				outG = (srcG*srcA + dstG*dstA*(1.0-srcA)) / outA
				outB = (srcB*srcA + dstB*dstA*(1.0-srcA)) / outA
			}
			clamp := func(v float64) uint32 {
				if v <= 0 {
					return 0
				}
				if v >= 255 {
					return 255
				}
				return uint32(v + 0.5)
			}
			pixels[idx] = clamp(outB) | (clamp(outG) << 8) | (clamp(outR) << 16) | (clamp(outA*255.0) << 24)
		}
	}

	hbmMask, _, _ := procCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, 0)
	if hbmMask == 0 {
		return 0
	}
	defer procDeleteObject.Call(hbmMask)

	ii := iconInfo{
		fIcon:    1,
		hbmMask:  hbmMask,
		hbmColor: hbmColor,
	}
	hOut, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	runtime.KeepAlive(&ii)
	return hOut
}

type trayIconSet struct {
	dpi          uint32
	baseSmall    uintptr
	enabledIcon  uintptr
	disabledIcon uintptr
	busyIcon     uintptr
	errorIcon    uintptr
}

func newBaseTrayIconSet(dpi uint32) trayIconSet {
	sz := smallIconSizeForDPI(dpi)
	return trayIconSet{dpi: dpi, baseSmall: loadEmbeddedIconAtSize(sz)}
}

func (s *trayIconSet) ensureBadges() {
	if s.baseSmall == 0 || (s.enabledIcon != 0 && s.disabledIcon != 0 && s.busyIcon != 0 && s.errorIcon != 0) {
		return
	}
	sz := smallIconSizeForDPI(s.dpi)
	if s.enabledIcon == 0 {
		s.enabledIcon = buildBadgedTrayIcon(s.baseSmall, sz, 16, 185, 129) // Emerald #10B981
	}
	if s.disabledIcon == 0 {
		s.disabledIcon = buildBadgedTrayIcon(s.baseSmall, sz, 120, 124, 135) // Neutral Slate
	}
	if s.busyIcon == 0 {
		s.busyIcon = buildBadgedTrayIcon(s.baseSmall, sz, 245, 158, 11) // Amber #F59E0B
	}
	if s.errorIcon == 0 {
		s.errorIcon = buildBadgedTrayIcon(s.baseSmall, sz, 239, 68, 68) // Red #EF4444
	}
}

func newTrayIconSet(dpi uint32) trayIconSet {
	set := newBaseTrayIconSet(dpi)
	set.ensureBadges()
	return set
}

func (s *trayIconSet) iconForState(status string, busy bool) uintptr {
	if busy && s.busyIcon != 0 {
		return s.busyIcon
	}
	switch status {
	case "enabled":
		if s.enabledIcon != 0 {
			return s.enabledIcon
		}
	case "disabled":
		if s.disabledIcon != 0 {
			return s.disabledIcon
		}
	default:
		if s.errorIcon != 0 {
			return s.errorIcon
		}
	}
	return s.baseSmall
}

func (s *trayIconSet) Destroy() {
	for _, h := range []*uintptr{&s.enabledIcon, &s.disabledIcon, &s.busyIcon, &s.errorIcon, &s.baseSmall} {
		if *h != 0 {
			procDestroyIcon.Call(*h)
			*h = 0
		}
	}
}
