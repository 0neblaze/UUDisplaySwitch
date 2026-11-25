# UUDisplaySwitch

极简原生 Windows 系统托盘工具，用于快速切换网易 UU 远程虚拟显示适配器（`GameViewer Virtual Display Adapter`，`ROOT\DISPLAY\0000`），解决开启虚拟屏后导致《无畏契约》（VALORANT）等全屏独占游戏分辨率或全屏显示异常的问题，并支持在 UU 退出后执行自动化操作。

全量采用纯 Go + Win32 原生 API 单文件构建，彻底移除 WinUI 3 与 .NET 运行时依赖，支持浅色/深色主题、三种语言（简中/繁中/英文）与事件驱动零轮询驻留。

本项目由个人独立开发，与网易及 UU 官方无关。

## 极简交互方式

- **双击 `UUDisplaySwitch.exe` 启动**：默认不弹出任何常驻主窗口，直接在任务栏通知区域（系统托盘）显示图标（绿色角标 = 已开启，灰色角标 = 已关闭）。
  - *注：Windows 11 默认将新托盘图标折叠在右下角 `^` 溢出面板内；首次使用可在「系统设置 -> 个性化 -> 任务栏 -> 其他系统托盘图标」中开启常显。*
- **左键单击托盘图标**：直接切换虚拟显示适配器的开启/关闭状态（本地未运行 `GameViewer.exe` 时无确认秒切；检测到 UU 远程运行且执行关闭时弹出原生防黑屏确认框）。
- **右键单击托盘图标**：打开深浅色自适应菜单，可打开**设置窗口**（关闭即彻底销毁释放全部 UI 资源）或**退出**（停止后台监测并移除图标）。

## 核心功能与低占用设计

- **精确设备身份保护**：严格锁定 `InstanceID == ROOT\DISPLAY\0000`、`GameViewer Virtual Display Adapter` 与显示适配器类别 GUID，绝不触碰物理独立/核心显卡。
- **事件驱动零空闲轮询**：设备状态通过 `CM_Register_Notification` 内核通知实时同步；当 UU 远程运行时直接对进程句柄执行内核 `WaitForMultipleObjects(INFINITE)` 零唤醒阻塞等待；状态文件仅在状态跃迁时写入磁盘。
- **单文件便携分发**：图标与 `PerMonitorV2` / `ComCtl32 v6` 清单直接内嵌于单个 `UUDisplaySwitch.exe`（约 `3.4 MiB`），无需额外运行库或外部资源文件。
- **可选开机监测**：启用「开机登录时自动启动后台监测」后，会将可执行文件复制到受 ACL 保护的 `%ProgramFiles%\UUDisplaySwitch\<UserSID>\` 目录并校验 SHA-256，注册登录后延迟 10 秒触发的计划任务。

## 下载与构建

可直接从 GitHub Releases 下载独立的单文件 `UUDisplaySwitch.exe`，放到任意位置双击即可运行（无需安装运行库或附带其他资源文件）。

如需从源码构建，仅需安装 PowerShell 与 Go（1.26 或更高版本）：

```powershell
pwsh -NoProfile -File .\build.ps1
```

脚本依次执行 `go test ./...`、`go vet ./...`、内嵌 PE 图标与清单资源（`./internal/genrsrc`），并在 `dist\UUDisplaySwitch.exe` 输出独立的单文件可执行程序。

## 验证

- **只读 UI 与句柄泄漏自检**（隔离命名空间，不改动真实设备、设置或开机任务）：
  ```powershell
  .\dist\UUDisplaySwitch.exe --read-only --validate-ui
  ```
- **状态查询与内存采样**：
  ```powershell
  .\dist\UUDisplaySwitch.exe --status
  pwsh -NoProfile -File .\tests\windows\Measure-Memory.ps1 -Label tray -OutputPath .\work\memory-tray.json -RequireWindowProcessAbsent
  ```

## 数据位置与许可

设置（`settings.json`）、监测状态（`monitor.json`）和操作记录（`actions.jsonl`）位于 `%LOCALAPPDATA%\UUDisplaySwitch`。自有源码按 [MIT License](LICENSE) 发布，第三方依赖说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
