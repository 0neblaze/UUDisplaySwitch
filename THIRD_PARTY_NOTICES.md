# Third-party components

This source repository does not vendor third-party package source or contain compiled runtime frameworks. Build tools restore the following direct dependency:

| Component | Version | Use | License / terms |
| --- | --- | --- | --- |
| `golang.org/x/sys` | `v0.49.0` | Go Windows system APIs | BSD 3-Clause; see the [upstream license](https://github.com/golang/sys/blob/v0.49.0/LICENSE). |

`build.ps1` compiles a standalone native Windows x64 executable (`UUDisplaySwitch.exe`) linking only built-in Windows system DLLs (`user32.dll`, `gdi32.dll`, `shell32.dll`, `cfgmgr32.dll`, `dwmapi.dll`, `uxtheme.dll`, `comctl32.dll`). The application is not endorsed by Microsoft, NetEase, or UU.
