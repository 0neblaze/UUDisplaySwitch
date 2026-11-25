package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

type userSettings struct {
	Theme       string `json:"theme"`
	Language    string `json:"language"`
	Backdrop    string `json:"backdrop,omitempty"`
	AutoDisable bool   `json:"autoDisable"`
	AutoExit    bool   `json:"autoExit"`
}

func defaultUserSettings() userSettings {
	return userSettings{
		Theme:    "system",
		Language: "zh-CN",
	}
}

func (s userSettings) monitorEnabled() bool {
	return s.AutoDisable || s.AutoExit
}

func settingsFilePath() string {
	return filepath.Join(dataDirectory(), "settings.json")
}

func loadUserSettings() (userSettings, error) {
	cfg := defaultUserSettings()
	data, err := os.ReadFile(settingsFilePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultUserSettings(), fmt.Errorf("invalid settings JSON: %w", err)
	}
	if cfg.Theme == "" {
		cfg.Theme = "system"
	}
	switch cfg.Theme {
	case "system", "light", "dark":
	default:
		return defaultUserSettings(), fmt.Errorf("invalid theme setting %q", cfg.Theme)
	}
	if cfg.Language == "" {
		cfg.Language = "zh-CN"
	}
	switch cfg.Language {
	case "zh-CN", "zh-TW", "en":
	default:
		return defaultUserSettings(), fmt.Errorf("invalid language setting %q", cfg.Language)
	}
	// Preserve legacy WinUI backdrop values ("none", "mica", "mica-alt") if present,
	// without failing if omitted or customized.
	return cfg, nil
}

func saveUserSettings(cfg userSettings) error {
	if err := os.MkdirAll(dataDirectory(), 0700); err != nil {
		return err
	}
	if err := writeJSONAtomically(settingsFilePath(), cfg); err != nil {
		return err
	}
	signalMonitorWake()
	return nil
}

func isSystemDarkTheme() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return val == 0
}

func effectiveDarkTheme(theme string) bool {
	switch theme {
	case "dark":
		return true
	case "light":
		return false
	default:
		return isSystemDarkTheme()
	}
}
