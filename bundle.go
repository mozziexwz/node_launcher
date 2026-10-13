//go:build bundle

package main

import "embed"

// Release builds (-tags bundle) embed the redirector runtime that CI builds
// into build/core; it is released to %ProgramData%\MSBOOST at run time.
//
//go:embed build/core/ProxyBridgeCore.dll build/core/WinDivert.dll build/core/WinDivert64.sys
var bundledRuntime embed.FS

const runtimeBundled = true
