package main

import (
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSettingsWindowLayoutNoOverlapOrVerticalClipping(t *testing.T) {
	const dtCalcrect = 0x00000400

	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		t.Fatal("GetDC(0) returned 0")
	}
	defer procReleaseDC.Call(0, hdc)

	measure := func(font uintptr, text string, rc winRect, flags uint32) winRect {
		oldFont, _, _ := procSelectObject.Call(hdc, font)
		defer func() {
			if oldFont != 0 {
				procSelectObject.Call(hdc, oldFont)
			}
		}()
		r := winRect{left: rc.left, top: rc.top, right: rc.right, bottom: rc.top}
		ptr, _ := windows.UTF16PtrFromString(text)
		negOne := ^uintptr(0)
		procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(ptr)), negOne, uintptr(unsafe.Pointer(&r)), uintptr(flags|dtCalcrect))
		runtime.KeepAlive(ptr)
		return r
	}

	dpis := []uint32{96, 120, 144, 192}
	langs := []string{"zh-CN", "zh-TW", "en"}
	const singleLine = dtLeft | dtSingleLine | dtNoPrefix
	const wrapFlags = dtLeft | dtWordBreak | dtNoPrefix

	for _, dpi := range dpis {
		for _, lang := range langs {
			sw := &settingsWindow{dpi: dpi}
			sw.layoutChildren()

			btnBottom := sw.rcStatusTitle.top + scalePx(2, dpi) + scalePx(30, dpi)
			if sw.rcDeviceSub.top < btnBottom {
				t.Errorf("dpi=%d lang=%s: rcDeviceSub.top (%d) overlaps action buttons bottom (%d)",
					dpi, lang, sw.rcDeviceSub.top, btnBottom)
			}
			clientH := scalePx(518, dpi)
			if sw.rcFooter.bottom > clientH {
				t.Errorf("dpi=%d lang=%s: rcFooter.bottom (%d) exceeds client height (%d)",
					dpi, lang, sw.rcFooter.bottom, clientH)
			}

			fontBold := createUIFont(10, 600, dpi, lang)
			fontHint := createUIFont(8, 400, dpi, lang)
			if fontBold == 0 || fontHint == 0 {
				if fontBold != 0 {
					procDeleteObject.Call(fontBold)
				}
				if fontHint != 0 {
					procDeleteObject.Call(fontHint)
				}
				t.Fatalf("dpi=%d lang=%s: createUIFont failed", dpi, lang)
			}

			s := getStrings(lang)
			subLabel := fmt.Sprintf("%s (%s)", deviceName, deviceID)
			checks := []struct {
				name  string
				font  uintptr
				text  string
				rc    winRect
				flags uint32
			}{
				{"rcHdrDevice", fontBold, s.sectionDevice, sw.rcHdrDevice, singleLine},
				{"rcHdrAuto", fontBold, s.sectionAutomation, sw.rcHdrAuto, singleLine},
				{"rcHdrAppearance", fontBold, s.sectionAppearance, sw.rcHdrAppearance, singleLine},
				{"rcDeviceSub", fontHint, subLabel, sw.rcDeviceSub, wrapFlags},
				{"rcFooter", fontHint, s.footerAbout, sw.rcFooter, wrapFlags},
			}
			for _, c := range checks {
				need := measure(c.font, c.text, c.rc, c.flags)
				needH := need.bottom - need.top
				allocH := c.rc.bottom - c.rc.top
				if needH > allocH {
					t.Errorf("dpi=%d lang=%s %s vertical overflow: needH=%d > allocH=%d (text=%q)",
						dpi, lang, c.name, needH, allocH, c.text)
				}
			}

			procDeleteObject.Call(fontBold)
			procDeleteObject.Call(fontHint)
		}
	}
}
