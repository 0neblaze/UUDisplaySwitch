package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

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

func dataDirectory() string { return filepath.Join(os.Getenv("LOCALAPPDATA"), "UUDisplaySwitch") }
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
func readState() appState {
	s := appState{Device: deviceName, CheckedAt: time.Now().Format("15:04:05"), GameRunning: gameRunning(), WatcherStatus: "stopped"}
	p, e := scanUU()
	if e == nil {
		s.UUStateKnown = true
		s.UURunning = p.Running
		s.UUUpdating = p.Updating
		s.UUExitAt = completionForUUState(p, nil, readUUExitCompletion())
	} else {
		s.UUStateError = e.Error()
	}
	if w, err := readHeartbeatForStatus(); err != nil {
		s.WatcherStatus = "error"
		s.WatcherError = err.Error()
		if mutexExists, mutexErr := monitorMutexExists(); mutexErr == nil {
			s.WatcherActive = mutexExists
		}
	} else if w != nil && w.Status != "stopped" {
		s.WatcherActive = true
		s.WatcherStatus = w.Status
		s.WatcherError = w.Error
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
func main() {
	apply := flag.String("apply", "", "enable or disable the verified device")
	status := flag.Bool("status", false, "read-only state in JSON")
	watch := flag.Bool("watch", false, "resident monitor for automatic disable/exit preferences")
	stopWatch := flag.Bool("stop-watch", false, "request the current verified automatic action monitor to stop")
	readOnly := flag.Bool("read-only", false, "block every mutation")
	runtimeInfo := flag.Bool("runtime-info", false, "read-only process identity and resolved monitor file path")
	flag.Parse()
	if *runtimeInfo {
		name, err := currentPackageName()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(10)
		}
		json.NewEncoder(os.Stdout).Encode(struct {
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
	if *readOnly && (*apply != "" || *watch || *stopWatch) {
		fmt.Fprintln(os.Stderr, "Read-only mode rejects mutations and monitor start/stop")
		os.Exit(13)
	}
	if err := requireDesktopRuntime(*watch); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(12)
	}
	if *status {
		s := readState()
		json.NewEncoder(os.Stdout).Encode(s)
		if s.Status == "error" {
			os.Exit(1)
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
	} else {
		fmt.Fprintln(os.Stderr, "Use the WinUI application, or --status, --watch, --stop-watch, or --apply")
		return
	}
	if err != nil {
		if *watch {
			if logErr := appendActivity(activity{Time: time.Now().Format(time.RFC3339), Mode: "monitor", Message: err.Error()}); logErr != nil {
				fmt.Fprintln(os.Stderr, "Could not save monitor failure:", logErr)
			}
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(10)
	}
}
