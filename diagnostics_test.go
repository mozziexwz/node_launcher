package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mozziexwz/node_launcher/internal/store"
)

func TestDiagnosticsNeverContainConfigContents(t *testing.T) {
	layout := store.NewLayout(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(filepath.Join("testdata", "client-example.json"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(layout.Configs, "直连.json"), cfg, 0o644)
	os.WriteFile(filepath.Join(layout.Configs, "broken.json"), []byte("{"), 0o644)
	os.WriteFile(filepath.Join(layout.Logs, "launcher-20261013.log"), []byte("2026-10-13 09:00:00 [程序] 加速已开始\n"), 0o644)
	os.WriteFile(layout.Settings, []byte(`{"version":1}`), 0o644)

	var buf bytes.Buffer
	if err := writeDiagnostics(&buf, layout, []string{"版本：test"}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]string)
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(b)
		if strings.Contains(string(b), "test-password") || strings.Contains(string(b), "test-user") {
			t.Fatalf("%s leaks node credentials", f.Name)
		}
	}
	for _, want := range []string{"system.txt", "settings.json", "configs.txt", "logs/launcher-20261013.log"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("bundle lacks %s; has %v", want, names)
		}
	}
	if !strings.Contains(names["configs.txt"], "直连.json　方案 default　入口 192.0.2.10:8964") ||
		!strings.Contains(names["configs.txt"], "broken.json　无效") {
		t.Fatalf("configs.txt:\n%s", names["configs.txt"])
	}
}
