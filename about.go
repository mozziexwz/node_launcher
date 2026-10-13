package main

import (
	"runtime/debug"
)

// version is set at release time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

const sourceURL = "https://github.com/mozziexwz/node_launcher"

// Versions of the non-Go components, fixed by the build.
const (
	redirectorVersion = "4.1.0（含 MSBOOST 补丁）"
	winDivertVersion  = "2.2.2-A"
)

// versionString is the release version plus the source revision when known.
func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	rev, dirty := "", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+"
			}
		}
	}
	if rev == "" {
		return version
	}
	return version + " (" + rev + dirty + ")"
}

func moduleVersion(path string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path == path {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return ""
}

// aboutInfo backs the About page. GPL-3.0 requires interactive programs to
// show the license notice; upstream component names appear only here.
func aboutInfo() AboutDTO {
	return AboutDTO{
		Version: versionString(),
		Source:  sourceURL,
		License: "MSBOOST 启动器是自由软件，依据 GNU 通用公共许可证第 3 版（GPL-3.0）发布。" +
			"本程序不提供任何担保。源代码与许可全文见项目主页。",
		Components: []ComponentDTO{
			{Name: "mieru", Version: moduleVersion("github.com/enfein/mieru/v3"), License: "GPL-3.0", URL: "https://github.com/enfein/mieru"},
			{Name: "ProxyBridge", Version: redirectorVersion, License: "MIT", URL: "https://github.com/InterceptSuite/ProxyBridge"},
			{Name: "WinDivert", Version: winDivertVersion, License: "LGPL-3.0", URL: "https://github.com/basil00/WinDivert"},
			{Name: "Wails", Version: moduleVersion("github.com/wailsapp/wails/v2"), License: "MIT", URL: "https://wails.io"},
			{Name: "fyne.io/systray", Version: moduleVersion("fyne.io/systray"), License: "Apache-2.0", URL: "https://github.com/fyne-io/systray"},
			{Name: "React", Version: "19", License: "MIT", URL: "https://react.dev"},
		},
	}
}
