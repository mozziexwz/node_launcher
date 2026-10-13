package main

import (
	"runtime/debug"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
// Wails builds with -buildvcs=false, so CI passes the commit this way too.
var version = "dev"

const copyrightNotice = "Copyright © 2026 MSBOOST"

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
// show a copyright notice, the absence of warranty and how to read the
// license. Component names, their copyright notices (LGPL-3.0 §4(c) for
// WinDivert) and the license texts are behind the "开源许可" dialog.
func aboutInfo() AboutDTO {
	return AboutDTO{
		Version:   versionString(),
		Copyright: copyrightNotice,
		License: "MSBOOST 启动器是自由软件：你可以依据自由软件基金会发布的 GNU 通用公共许可证第 3 版（GPL-3.0）" +
			"再发布和修改本程序。本程序不提供任何担保。许可全文和所用的开源组件见“开源许可”。",
		Components: []ComponentDTO{
			// Shown by its Japanese name, as the project itself writes it; the
			// UI never says mieru.
			{Name: "見える", Version: moduleVersion("github.com/enfein/mieru/v3"), Copyright: "Copyright (C) 2021-2026 見える 作者",
				License: "GPL-3.0", URL: "https://github.com/enfein/mieru"},
			{Name: "ProxyBridge", Version: redirectorVersion, Copyright: "Copyright (c) 2025 Anof-cyber/InterceptSuite",
				License: "MIT", URL: "https://github.com/InterceptSuite/ProxyBridge"},
			{Name: "WinDivert", Version: winDivertVersion, Copyright: "Copyright (C) 2019 basil",
				License: "LGPL-3.0", URL: "https://github.com/basil00/WinDivert"},
			{Name: "Wails", Version: moduleVersion("github.com/wailsapp/wails/v2"), Copyright: "Copyright (c) 2018-Present Lea Anthony",
				License: "MIT", URL: "https://wails.io"},
			{Name: "fyne.io/systray", Version: moduleVersion("fyne.io/systray"), Copyright: "fyne.io/systray 与 getlantern/systray 作者",
				License: "Apache-2.0", URL: "https://github.com/fyne-io/systray"},
			{Name: "React", Version: "19", Copyright: "Copyright (c) Meta Platforms, Inc. and affiliates.",
				License: "MIT", URL: "https://react.dev"},
		},
		LicenseTexts: []LinkDTO{
			{Name: "GPL-3.0", URL: "https://www.gnu.org/licenses/gpl-3.0.html"},
			{Name: "LGPL-3.0", URL: "https://www.gnu.org/licenses/lgpl-3.0.html"},
			{Name: "Apache-2.0", URL: "https://www.apache.org/licenses/LICENSE-2.0"},
			{Name: "MIT", URL: "https://opensource.org/license/mit"},
		},
	}
}
