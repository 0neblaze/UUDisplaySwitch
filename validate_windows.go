package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetGuiResources = user32Tray.NewProc("GetGuiResources")

func currentProcessGuiCounts() (gdi, user uint32) {
	if procGetGuiResources.Find() != nil {
		return 0, 0
	}
	h := uintptr(windows.CurrentProcess())
	g, _, _ := procGetGuiResources.Call(h, 0) // GR_GDIOBJECTS
	u, _, _ := procGetGuiResources.Call(h, 1) // GR_USEROBJECTS
	return uint32(g), uint32(u)
}

func validateStartupPlanReadOnly() (map[string]any, error) {
	sid, err := currentUserSIDString()
	if err != nil {
		return nil, err
	}
	targetNew := protectedMonitorPathForSID(sid)
	targetLegacy := legacyProtectedMonitorPathForSID(sid)

	xmlNew := createStartupTaskXML(sid, targetNew)
	if enabled, err := verifyOwnedStartupTaskXML([]byte(xmlNew), sid); err != nil || !enabled {
		return nil, fmt.Errorf("new startup task XML rejected: enabled=%v err=%v", enabled, err)
	}
	xmlLegacy := createStartupTaskXML(sid, targetLegacy)
	if enabled, err := verifyOwnedStartupTaskXML([]byte(xmlLegacy), sid); err != nil || !enabled {
		return nil, fmt.Errorf("legacy startup task XML rejected: enabled=%v err=%v", enabled, err)
	}

	rejected := 0
	for _, invalid := range []string{
		strings.Replace(xmlNew, "--watch", "--apply disable", 1),
		strings.Replace(xmlNew, startupTaskSource, "OtherApp", 1),
		strings.Replace(xmlNew, "HighestAvailable", "LeastPrivilege", 1),
	} {
		if _, err := verifyOwnedStartupTaskXML([]byte(invalid), sid); err != nil {
			rejected++
		}
	}
	if rejected != 3 {
		return nil, fmt.Errorf("expected 3 invalid startup plans rejected, got %d", rejected)
	}
	return map[string]any{
		"schemaValid":          true,
		"legacyCoreCompatible": true,
		"rejectedPlans":        rejected,
		"protectedExecutable":  targetNew,
	}, nil
}

func runReadOnlyUIValidation() error {
	if !globalReadOnly {
		return errors.New("--validate-ui requires --read-only")
	}
	shellClass, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	shellTray, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(shellClass)), 0)
	runtime.KeepAlive(shellClass)
	if shellTray == 0 {
		return errors.New("interactive taskbar window (Shell_TrayWnd) not found; run --validate-ui in an interactive desktop session")
	}
	if os.Getenv("UUDISPLAYSWITCH_TEST_NS") == "" {
		_ = os.Setenv("UUDISPLAYSWITCH_TEST_NS", fmt.Sprintf("validate-%d", os.Getpid()))
	}

	startupReport, err := validateStartupPlanReadOnly()
	if err != nil {
		return err
	}

	var validationErr error
	var summary map[string]any

	trayErr := runResidentTray(false, func(tray *residentTray) {
		go func() {
			defer func() {
				tray.postUIUpdate(func() {
					_ = tray.shutdown()
				})
			}()

			waitStep := func() { time.Sleep(45 * time.Millisecond) }
			runSync := func(fn func() error) error {
				done := make(chan error, 1)
				tray.postUIUpdate(func() {
					done <- fn()
				})
				return <-done
			}

			if st := trayStatusNow(); !st.Ready {
				validationErr = fmt.Errorf("tray not ready during validation: %+v", st)
				return
			}

			// Exercise all 3 languages x 2 themes on the native Win32 Settings window.
			for _, lang := range []string{"zh-CN", "zh-TW", "en"} {
				for _, theme := range []string{"light", "dark"} {
					if err := runSync(func() error {
						tray.settings.Language = lang
						tray.settings.Theme = theme
						tray.openOrFocusSettings()
						if tray.settingsWin == nil || tray.settingsWin.hwnd == 0 {
							return fmt.Errorf("settings window failed to open for %s/%s", lang, theme)
						}
						tray.settingsWin.rebuildThemeAndFonts()
						tray.settingsWin.populateTextsAndValues()
						tray.settingsWin.layoutChildren()
						return tray.refreshIconAndTooltip()
					}); err != nil {
						validationErr = err
						return
					}
					waitStep()
				}
			}

			// Close window and confirm complete destruction.
			if err := runSync(func() error {
				if tray.settingsWin != nil && tray.settingsWin.hwnd != 0 {
					procDestroyWindow.Call(tray.settingsWin.hwnd)
				}
				if tray.settingsWin != nil {
					return errors.New("settingsWin was not cleared on DestroyWindow")
				}
				return nil
			}); err != nil {
				validationErr = err
				return
			}
			waitStep()

			// Measure GDI & USER object stability across 6 open -> close cycles.
			gdiBefore, userBefore := currentProcessGuiCounts()
			for i := 0; i < 6; i++ {
				if err := runSync(func() error {
					tray.openOrFocusSettings()
					if tray.settingsWin == nil || tray.settingsWin.hwnd == 0 {
						return fmt.Errorf("cycle %d failed to create settings window", i)
					}
					return nil
				}); err != nil {
					validationErr = err
					return
				}
				waitStep()
				if err := runSync(func() error {
					procDestroyWindow.Call(tray.settingsWin.hwnd)
					if tray.settingsWin != nil {
						return fmt.Errorf("cycle %d leaked settingsWin pointer", i)
					}
					return nil
				}); err != nil {
					validationErr = err
					return
				}
				waitStep()
			}
			gdiAfter, userAfter := currentProcessGuiCounts()
			if int(gdiAfter)-int(gdiBefore) > 4 || int(userAfter)-int(userBefore) > 4 {
				validationErr = fmt.Errorf("GDI/USER handle growth detected across 6 open/close cycles: gdi %d->%d, user %d->%d",
					gdiBefore, gdiAfter, userBefore, userAfter)
				return
			}

			summary = map[string]any{
				"readOnly":            true,
				"trayReady":           true,
				"languagesTested":     3,
				"themesTested":        2,
				"openCloseCycles":     6,
				"settingsReleased":    tray.settingsWin == nil,
				"gdiObjectsBefore":    gdiBefore,
				"gdiObjectsAfter":     gdiAfter,
				"userObjectsBefore":   userBefore,
				"userObjectsAfter":    userAfter,
				"startupValidation":   startupReport,
			}
		}()
	})
	if trayErr != nil {
		return trayErr
	}
	if validationErr != nil {
		return validationErr
	}
	if outPath := os.Getenv("UUDISPLAYSWITCH_VALIDATION_OUT"); outPath != "" {
		_ = writeJSONAtomically(outPath, summary)
	}
	return writeStdoutJSON(summary)
}
