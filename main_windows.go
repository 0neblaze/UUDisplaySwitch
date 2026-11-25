package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func writeStdoutJSON(v any) error {
	err := json.NewEncoder(os.Stdout).Encode(v)
	if errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		return nil
	}
	return err
}

var globalReadOnly bool

type activity struct {
	Time    string `json:"time"`
	Mode    string `json:"mode"`
	OK      bool   `json:"ok"`
	Problem uint32 `json:"problem"`
	Message string `json:"message"`
}

type appState struct {
	Status        string `json:"status"`
	Device        string `json:"device"`
	Problem       uint32 `json:"problem"`
	Message       string `json:"message"`
	CheckedAt     string `json:"checkedAt"`
	GameRunning   bool   `json:"gameRunning"`
	UURunning     bool   `json:"uuRunning"`
	UUUpdating    bool   `json:"uuUpdating"`
	UUStateKnown  bool   `json:"uuStateKnown"`
	UUStateError  string `json:"uuStateError"`
	WatcherActive bool   `json:"watcherActive"`
	WatcherStatus string `json:"watcherStatus"`
	WatcherError  string `json:"watcherError"`
	UUExitAt      int64  `json:"uuExitAt"`
}

func dataDirectory() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "UUDisplaySwitch")
}

func appendActivity(a activity) error {
	dir := dataDirectory()
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	p := filepath.Join(dir, "actions.jsonl")
	if s, e := os.Stat(p); e == nil && s.Size() > 256*1024 {
		old := p + ".previous"
		if e = os.Remove(old); e != nil && !os.IsNotExist(e) {
			return e
		}
		if e = os.Rename(p, old); e != nil {
			return e
		}
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(a)
}

func readDeviceOnlyState() appState {
	s := appState{
		Device:        deviceName,
		CheckedAt:     time.Now().Format("15:04:05"),
		WatcherStatus: "stopped",
	}
	d, e := (nativeDevice{}).Read()
	if e != nil {
		s.Status = "error"
		s.Message = e.Error()
		return s
	}
	s.Problem = d.Problem
	switch d.Problem {
	case 0:
		s.Status = "enabled"
	case 22:
		s.Status = "disabled"
	default:
		s.Status = "error"
		s.Message = fmt.Sprintf("Device problem %d", d.Problem)
	}
	return s
}

func readState(includeGame bool) appState {
	s := readDeviceOnlyState()
	p, gameActive, e := scanUUAndGame(includeGame)
	if includeGame {
		s.GameRunning = gameActive
	}
	w, hbErr := readHeartbeatForStatus()
	if e == nil {
		s.UUStateKnown = true
		s.UURunning = p.Running
		s.UUUpdating = p.Updating
		var exitAt int64
		if hbErr == nil && w != nil {
			exitAt = completedUUExitAt(*w)
		}
		s.UUExitAt = completionForUUState(p, nil, exitAt)
	} else {
		s.UUStateError = e.Error()
	}
	if hbErr != nil {
		s.WatcherStatus = "error"
		s.WatcherError = hbErr.Error()
		if mutexExists, mutexErr := monitorMutexExists(); mutexErr == nil {
			s.WatcherActive = mutexExists
		}
	} else if w != nil && w.Status != "stopped" {
		s.WatcherActive = true
		s.WatcherStatus = w.Status
		s.WatcherError = w.Error
	}
	return s
}

func main() {
	apply := flag.String("apply", "", "enable or disable the verified device")
	status := flag.Bool("status", false, "read-only state in JSON")
	watch := flag.Bool("watch", false, "resident monitor for automatic disable/exit preferences")
	stopWatch := flag.Bool("stop-watch", false, "request the current verified automatic action monitor to stop")
	tray := flag.Bool("tray", false, "start the resident notification-area tray")
	stopTray := flag.Bool("stop-tray", false, "request the verified resident tray to stop")
	trayStatus := flag.Bool("tray-status", false, "read-only resident tray readiness in JSON")
	openSettings := flag.Bool("settings", false, "start or focus the resident tray and open the settings window")
	startupMode := flag.String("startup", "", "internal elevated helper: enable or disable sign-in startup task")
	startupOwner := flag.String("owner", "", "expected user SID for --startup")
	startupRequest := flag.String("request", "", "request token for --startup")
	readOnly := flag.Bool("read-only", false, "block every device, monitor, and startup mutation")
	validateUI := flag.Bool("validate-ui", false, "run read-only native tray and settings validation")
	runtimeInfo := flag.Bool("runtime-info", false, "read-only process identity and resolved monitor file path")
	flag.Parse()

	globalReadOnly = *readOnly

	if *runtimeInfo {
		name, err := currentPackageName()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(10)
		}
		_ = writeStdoutJSON(struct {
			PID              int    `json:"pid"`
			Package          string `json:"package"`
			DataDirectory    string `json:"dataDirectory"`
			EffectiveMonitor string `json:"effectiveMonitor"`
		}{os.Getpid(), name, dataDirectory(), func() string {
			p, e := effectiveMonitorPath()
			if e != nil {
				return e.Error()
			}
			return p
		}()})
		return
	}

	if *startupMode != "" {
		os.Exit(runStartupHelperCommand(*startupMode, *startupOwner, *startupRequest))
	}

	if *readOnly && (*apply != "" || *watch || *stopWatch) {
		fmt.Fprintln(os.Stderr, "Read-only mode rejects mutations and monitor start/stop")
		os.Exit(13)
	}

	if *validateUI {
		if err := runReadOnlyUIValidation(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *trayStatus {
		if err := mainTrayCommand(true, false, false, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(10)
		}
		return
	}

	if *status {
		if err := requireDesktopRuntime(false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(12)
		}
		s := readState(true)
		_ = writeStdoutJSON(s)
		if s.Status == "error" {
			os.Exit(1)
		}
		return
	}

	// Default launch (double-clicking UUDisplaySwitch.exe with no flags) starts the resident tray.
	defaultTrayStart := !*watch && !*stopWatch && *apply == "" && !*stopTray
	isTrayAction := *tray || *stopTray || *openSettings || defaultTrayStart

	if !isTrayAction || *stopTray {
		if err := requireDesktopRuntime(!*readOnly && *watch); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(12)
		}
	}

	if isTrayAction {
		start := !*stopTray
		err := mainTrayCommand(false, start, *stopTray, *openSettings)
		if err != nil {
			if start && !*readOnly {
				_ = appendActivity(activity{Time: time.Now().Format(time.RFC3339), Mode: "tray-start", Message: err.Error()})
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(10)
		}
		return
	}

	var err error
	if *watch {
		err = watchUU()
	} else if *stopWatch {
		err = stopUUWatch()
	} else if *apply != "" {
		err = runHelper(*apply)
	}
	if err != nil {
		if *watch && !*readOnly {
			_ = appendActivity(activity{Time: time.Now().Format(time.RFC3339), Mode: "monitor", Message: err.Error()})
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(10)
	}
}
