# Third-party components

This source repository does not vendor third-party package source or contain compiled application/runtime packages. Build tools restore the following direct dependencies:

| Component | Version | Use | License / terms |
| --- | --- | --- | --- |
| `golang.org/x/sys` | `v0.49.0` | Go Windows system APIs | BSD 3-Clause; see the [upstream license](https://github.com/golang/sys/blob/v0.49.0/LICENSE). |
| `Microsoft.WindowsAppSDK` | `1.8.260921001` | WinUI 3 and Windows App SDK APIs | Microsoft Software License Terms; see the [package license](https://www.nuget.org/packages/Microsoft.WindowsAppSDK/1.8.260921001/License). |
| `Microsoft.Windows.SDK.BuildTools` | `10.0.28000.2705` | Windows SDK build tools | Microsoft package terms; see the [NuGet package page](https://www.nuget.org/packages/Microsoft.Windows.SDK.BuildTools/10.0.28000.2705) and its License Info link. |

The `build.ps1` output is self-contained and may include files placed by the Windows App SDK NuGet package. Those files are not included in this source release. Anyone distributing a built application must review and comply with the licenses for the exact direct and transitive packages in that build, including any redistribution conditions and required end-user terms. The application is not endorsed by Microsoft, NetEase, or UU.
