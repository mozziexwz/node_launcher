package main

import (
	"io/fs"
	"path/filepath"

	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/runtimefiles"
	"github.com/mozziexwz/node_launcher/internal/store"
	"github.com/mozziexwz/node_launcher/internal/winsys"
)

// Files of the redirector runtime, checked in this order.
var runtimeNames = []string{"ProxyBridgeCore.dll", "WinDivert.dll", "WinDivert64.sys"}

// runtimeSource says where the redirector's files come from.
type runtimeSource struct {
	corePath string
	prepare  func() (release func(), err error) // nil in development builds
	describe string
}

func loadRuntime(layout store.Layout, log *logx.Logger) (runtimeSource, error) {
	if !runtimeBundled {
		return runtimeSource{
			corePath: filepath.Join(layout.Root, "ProxyBridgeCore.dll"),
			describe: "开发版，从程序目录加载",
		}, nil
	}
	files, err := fs.Sub(bundledRuntime, "build/core")
	if err != nil {
		return runtimeSource{}, err
	}
	m, err := runtimefiles.New(files, runtimeNames, winsys.ProgramDataDir())
	if err != nil {
		return runtimeSource{}, err
	}
	m.Logf = func(format string, args ...any) { log.Printf(logx.App, format, args...) }
	return runtimeSource{corePath: m.Path("ProxyBridgeCore.dll"), prepare: m.Ensure, describe: m.Describe()}, nil
}
