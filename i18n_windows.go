package main

type uiStrings struct {
	app                  string
	tipEnabled           string
	tipDisabled          string
	tipSwitching         string
	tipUnavailable       string
	menuStatusFmt        string
	menuSettings         string
	menuExit             string
	alreadyRunningTitle  string
	alreadyRunningBody   string
	confirmDisableTitle  string
	confirmDisableBody   string
	gameRestartTitle     string
	gameRestartBody      string
	readOnlyBlocked      string
	errorTitle           string
	settingsTitle        string
	sectionDevice        string
	deviceEnabled        string
	deviceDisabled       string
	deviceUnavailable    string
	deviceSwitching      string
	btnEnable            string
	btnDisable           string
	btnRefresh           string
	btnClose             string
	monitorPrefix        string
	monitorStopped       string
	monitorArmed         string
	monitorWaiting       string
	monitorGrace         string
	monitorDone          string
	monitorUpdating      string
	monitorError         string
	sectionAutomation    string
	optAutoDisable       string
	optAutoDisableHint   string
	optAutoExit          string
	optAutoExitHint      string
	optStartup           string
	optStartupHint       string
	sectionAppearance    string
	lblTheme             string
	themeSystem          string
	themeLight           string
	themeDark            string
	lblLanguage          string
	footerAbout          string
	savedError           string
	startupError         string
}

func normalizeLanguage(lang string) string {
	switch lang {
	case "zh-TW", "en":
		return lang
	default:
		return "zh-CN"
	}
}

func getStrings(lang string) uiStrings {
	switch normalizeLanguage(lang) {
	case "zh-TW":
		return uiStrings{
			app:                 "UU 虛擬螢幕",
			tipEnabled:          "UU 虛擬螢幕：已開啟（左鍵點擊關閉 · 右鍵設定）",
			tipDisabled:         "UU 虛擬螢幕：已關閉（左鍵點擊開啟 · 右鍵設定）",
			tipSwitching:        "UU 虛擬螢幕：正在切換…",
			tipUnavailable:      "UU 虛擬螢幕：狀態異常（右鍵開啟設定檢視）",
			menuStatusFmt:       "虛擬螢幕：%s · 自動監測：%s",
			menuSettings:        "設定…",
			menuExit:            "退出",
			alreadyRunningTitle: "UU 虛擬螢幕已在背景執行",
			alreadyRunningBody:  "左鍵點擊托盤圖示可直接切換開關，右鍵點擊可開啟設定或退出。",
			confirmDisableTitle: "關閉虛擬介面卡？",
			confirmDisableBody:  "偵測到 UU 遠端（GameViewer）正在執行。現在關閉虛擬顯示介面卡可能導致遠端畫面黑屏斷線。\n\n確定仍要關閉嗎？",
			gameRestartTitle:    "已完成切換",
			gameRestartBody:     "偵測到受虛擬顯示器影響的全螢幕遊戲正在執行，請完整退出並重新啟動遊戲後再選擇全螢幕。",
			readOnlyBlocked:     "唯讀檢查模式：不會切換裝置或修改自動監測。",
			errorTitle:          "UU 虛擬螢幕",
			settingsTitle:       "UU 虛擬螢幕 — 設定",
			sectionDevice:       "當前裝置狀態",
			deviceEnabled:       "已開啟",
			deviceDisabled:      "已關閉",
			deviceUnavailable:   "暫時無法使用",
			deviceSwitching:     "正在切換…",
			btnEnable:           "開啟虛擬螢幕",
			btnDisable:          "關閉虛擬螢幕",
			btnRefresh:          "重新整理",
			btnClose:            "關閉",
			monitorPrefix:       "背景監測：",
			monitorStopped:      "未執行",
			monitorArmed:        "執行中 · 等待 UU 退出",
			monitorWaiting:      "執行中 · 等待 UU 啟動",
			monitorGrace:        "正在確認 UU 已退出…",
			monitorDone:         "已處理退出 · 繼續監測",
			monitorUpdating:     "UU 正在更新 · 暫緩切換",
			monitorError:        "監測異常",
			sectionAutomation:   "UU 退出自動化",
			optAutoDisable:      "退出 UU 遠端後，停用虛擬顯示介面卡",
			optAutoDisableHint:  "僅在 UU 主程式退出滿 6 秒後觸發；關閉視窗或縮至系統匣不會觸發。",
			optAutoExit:         "退出 UU 遠端後，自動退出本工具",
			optAutoExitHint:     "兩項同時開啟時，先成功停用介面卡再自動退出。",
			optStartup:          "登入 Windows 時自動啟動背景監測",
			optStartupHint:      "登入後延遲 10 秒靜默啟動監測（須開啟上方至少一項自動化）。",
			sectionAppearance:   "外觀與語言",
			lblTheme:            "應用主題",
			themeSystem:         "跟隨系統",
			themeLight:          "淺色",
			themeDark:           "深色",
			lblLanguage:         "顯示語言",
			footerAbout:         "UUDisplaySwitch v2.4.0 · 原生極簡系統匣版（僅控制 ROOT\\DISPLAY\\0000）",
			savedError:          "無法儲存設定：",
			startupError:        "無法更新開機監測：",
		}
	case "en":
		return uiStrings{
			app:                 "UU Display",
			tipEnabled:          "UU Display: Enabled (Left-click to disable · Right-click for settings)",
			tipDisabled:         "UU Display: Disabled (Left-click to enable · Right-click for settings)",
			tipSwitching:        "UU Display: Switching…",
			tipUnavailable:      "UU Display: Unavailable (Right-click for settings)",
			menuStatusFmt:       "Adapter: %s · Monitor: %s",
			menuSettings:        "Settings…",
			menuExit:            "Exit",
			alreadyRunningTitle: "UU Display is running in the system tray",
			alreadyRunningBody:  "Left-click the tray icon to toggle the adapter; right-click for Settings or Exit.",
			confirmDisableTitle: "Disable virtual display adapter?",
			confirmDisableBody:  "UU Remote (GameViewer) is currently running. Disabling the virtual display adapter now may black out your remote session.\n\nDisable anyway?",
			gameRestartTitle:    "Switch completed",
			gameRestartBody:     "An affected fullscreen game is running. Fully exit and restart the game before selecting fullscreen.",
			readOnlyBlocked:     "Read-only mode: device changes and startup updates are blocked.",
			errorTitle:          "UU Display",
			settingsTitle:       "UU Display — Settings",
			sectionDevice:       "Adapter Status",
			deviceEnabled:       "Enabled",
			deviceDisabled:      "Disabled",
			deviceUnavailable:   "Unavailable",
			deviceSwitching:     "Switching…",
			btnEnable:           "Enable Adapter",
			btnDisable:          "Disable Adapter",
			btnRefresh:          "Refresh",
			btnClose:            "Close",
			monitorPrefix:       "Monitor: ",
			monitorStopped:      "Stopped",
			monitorArmed:        "Running · waiting for UU exit",
			monitorWaiting:      "Running · waiting for UU start",
			monitorGrace:        "Confirming UU exit…",
			monitorDone:         "Exit handled · monitoring",
			monitorUpdating:     "UU updating · paused",
			monitorError:        "Monitor error",
			sectionAutomation:   "When UU Remote Exits",
			optAutoDisable:      "Disable virtual display adapter on UU exit",
			optAutoDisableHint:  "Triggers 6s after the main GameViewer process exits; minimizing to tray is ignored.",
			optAutoExit:         "Exit this tool on UU exit",
			optAutoExitHint:     "When both are enabled, the adapter is disabled first before exiting.",
			optStartup:          "Start background monitor at Windows sign-in",
			optStartupHint:      "Silently starts monitoring 10s after sign-in (enable at least one option above).",
			sectionAppearance:   "Appearance & Language",
			lblTheme:            "Theme",
			themeSystem:         "System default",
			themeLight:          "Light",
			themeDark:           "Dark",
			lblLanguage:         "Language",
			footerAbout:         "UUDisplaySwitch v2.4.0 · Native lightweight tray utility (ROOT\\DISPLAY\\0000 only)",
			savedError:          "Could not save settings: ",
			startupError:        "Could not update sign-in startup: ",
		}
	default:
		return uiStrings{
			app:                 "UU 虚拟屏",
			tipEnabled:          "UU 虚拟屏：已开启（左键点击关闭 · 右键设置）",
			tipDisabled:         "UU 虚拟屏：已关闭（左键点击开启 · 右键设置）",
			tipSwitching:        "UU 虚拟屏：正在切换…",
			tipUnavailable:      "UU 虚拟屏：状态异常（右键打开设置查看）",
			menuStatusFmt:       "虚拟屏：%s · 自动监测：%s",
			menuSettings:        "设置…",
			menuExit:            "退出",
			alreadyRunningTitle: "UU 虚拟屏已在后台运行",
			alreadyRunningBody:  "左键点击任务栏托盘图标可直接切换开关，右键点击可打开设置或退出。",
			confirmDisableTitle: "关闭虚拟适配器？",
			confirmDisableBody:  "检测到 UU 远程（GameViewer）正在运行。现在关闭虚拟显示适配器可能导致远端画面黑屏断连。\n\n确定仍要关闭吗？",
			gameRestartTitle:    "已完成切换",
			gameRestartBody:     "检测到受虚拟显示器影响的全屏游戏正在运行，请完整退出并重新启动游戏后再选择全屏。",
			readOnlyBlocked:     "只读检查模式：不会切换设备或修改自动监测。",
			errorTitle:          "UU 虚拟屏",
			settingsTitle:       "UU 虚拟屏 — 设置",
			sectionDevice:       "当前设备状态",
			deviceEnabled:       "已开启",
			deviceDisabled:      "已关闭",
			deviceUnavailable:   "暂时不可用",
			deviceSwitching:     "正在切换…",
			btnEnable:           "开启虚拟屏",
			btnDisable:          "关闭虚拟屏",
			btnRefresh:          "刷新状态",
			btnClose:            "关闭",
			monitorPrefix:       "后台监测：",
			monitorStopped:      "未运行",
			monitorArmed:        "运行中 · 等待 UU 退出",
			monitorWaiting:      "运行中 · 等待 UU 启动",
			monitorGrace:        "正在确认 UU 已退出…",
			monitorDone:         "已处理退出 · 继续监测",
			monitorUpdating:     "UU 正在更新 · 暂缓切换",
			monitorError:        "监测异常",
			sectionAutomation:   "UU 退出自动化",
			optAutoDisable:      "退出 UU 远程后，禁用虚拟显示适配器",
			optAutoDisableHint:  "仅在 UU 主程序退出满 6 秒后触发；关闭窗口或缩到托盘不会触发。",
			optAutoExit:         "退出 UU 远程后，自动退出本工具",
			optAutoExitHint:     "两项同时开启时，先成功禁用适配器再自动退出本工具。",
			optStartup:          "开机登录时自动启动后台监测",
			optStartupHint:      "登录 Windows 10 秒后静默启动监测（须开启上方至少一项自动化）。",
			sectionAppearance:   "外观与语言",
			lblTheme:            "应用主题",
			themeSystem:         "跟随系统",
			themeLight:          "浅色",
			themeDark:           "深色",
			lblLanguage:         "显示语言",
			footerAbout:         "UUDisplaySwitch v2.4.0 · 原生极简托盘版（仅控制 ROOT\\DISPLAY\\0000）",
			savedError:          "无法保存设置：",
			startupError:        "无法更新开机监测：",
		}
	}
}

func monitorStatusText(s uiStrings, state appState) string {
	if state.WatcherStatus == "error" {
		if state.WatcherError != "" {
			return s.monitorError + " (" + state.WatcherError + ")"
		}
		return s.monitorError
	}
	if !state.WatcherActive {
		return s.monitorStopped
	}
	switch state.WatcherStatus {
	case "armed":
		return s.monitorArmed
	case "waiting", "starting":
		return s.monitorWaiting
	case "grace":
		return s.monitorGrace
	case "done":
		return s.monitorDone
	case "updating":
		return s.monitorUpdating
	default:
		return s.monitorWaiting
	}
}
