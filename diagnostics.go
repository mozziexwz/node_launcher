package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mozziexwz/node_launcher/internal/store"
)

// writeDiagnostics writes a support bundle: the system summary, settings, a
// list of node configurations (names and entrances only, never their
// contents or passwords) and the launcher logs, which are masked already.
func writeDiagnostics(w io.Writer, layout store.Layout, summary []string) error {
	zw := zip.NewWriter(w)
	add := func(name string, data []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}

	if err := add("system.txt", []byte(strings.Join(summary, "\r\n")+"\r\n")); err != nil {
		return err
	}
	if b, err := os.ReadFile(layout.Settings); err == nil {
		if err := add("settings.json", b); err != nil {
			return err
		}
	}

	var lines []string
	if list, err := layout.ListConfigs(); err != nil {
		lines = append(lines, "无法读取配置目录："+err.Error())
	} else {
		for _, c := range list {
			if c.Err != nil {
				lines = append(lines, fmt.Sprintf("%s　无效：%v", c.Name, c.Err))
				continue
			}
			lines = append(lines, fmt.Sprintf("%s　方案 %s　入口 %s", c.Name, c.Profile, c.Entrance))
		}
	}
	if err := add("configs.txt", []byte(strings.Join(lines, "\r\n")+"\r\n")); err != nil {
		return err
	}

	logs, _ := filepath.Glob(filepath.Join(layout.Logs, "launcher-*.log"))
	sort.Strings(logs)
	for _, path := range logs {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := add("logs/"+filepath.Base(path), b); err != nil {
			return err
		}
	}
	return zw.Close()
}
