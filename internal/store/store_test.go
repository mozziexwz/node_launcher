package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exampleConfig(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "client-example.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsDefaultsAndRoundTrip(t *testing.T) {
	l := NewLayout(t.TempDir())
	s, err := l.LoadSettings()
	if err != nil {
		t.Fatalf("missing settings must not be an error: %v", err)
	}
	if s.ProbeTarget.Address != "35.155.204.207" || s.ProbeTarget.Port != 8585 {
		t.Fatalf("default probe target %+v", s.ProbeTarget)
	}
	if s.Addons["discord"] || s.Addons["steam"] || len(s.Addons) != 2 {
		t.Fatalf("addons must exist and default to off: %v", s.Addons)
	}

	if s.Transport != "" {
		t.Fatalf("default transport %q, want empty (TCP)", s.Transport)
	}

	s.CurrentConfig = "直连.json"
	s.ProbeTarget = ProbeTarget{Address: "51.222.56.192", Port: 8484}
	s.Addons["discord"] = true
	s.Transport = "udp"
	if err := l.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	got, err := l.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentConfig != "直连.json" || got.ProbeTarget.Port != 8484 || !got.Addons["discord"] || got.Addons["steam"] || got.Transport != "udp" {
		t.Fatalf("round trip %+v", got)
	}
}

func TestSettingsRejectInvalidValues(t *testing.T) {
	l := NewLayout(t.TempDir())
	for name, mutate := range map[string]func(*Settings){
		"private probe target": func(s *Settings) { s.ProbeTarget.Address = "10.0.0.1" },
		"bad probe port":       func(s *Settings) { s.ProbeTarget.Port = 70000 },
		"unknown addon":        func(s *Settings) { s.Addons["vpn"] = true },
		"path in config name":  func(s *Settings) { s.CurrentConfig = `..\x.json` },
		"unknown transport":    func(s *Settings) { s.Transport = "quic" },
	} {
		s := DefaultSettings()
		mutate(&s)
		if err := l.SaveSettings(s); err == nil {
			t.Errorf("%s: SaveSettings accepted invalid settings", name)
		}
	}
}

func TestSettingsCorruptFileFallsBackToDefaults(t *testing.T) {
	l := NewLayout(t.TempDir())
	writeFile(t, l.Settings, []byte("{not json"))
	s, err := l.LoadSettings()
	if err == nil {
		t.Fatal("a corrupt file must be reported")
	}
	if s.ProbeTarget != DefaultSettings().ProbeTarget {
		t.Fatalf("corrupt settings must fall back to defaults: %+v", s)
	}
	writeFile(t, l.Settings, []byte(`{"version":1,"probeTarget":{"address":"192.168.1.1","port":1}}`))
	if _, err := l.LoadSettings(); err == nil {
		t.Fatal("an invalid probe target must be reported")
	}
	writeFile(t, l.Settings, []byte(`{"version":1,"probeTarget":{"address":"51.222.56.192","port":8484}}`))
	s, err = l.LoadSettings()
	if err != nil || len(s.Addons) != 2 {
		t.Fatalf("missing addon keys must be filled: %+v %v", s, err)
	}
}

func TestImportListDeleteConfigs(t *testing.T) {
	l := NewLayout(t.TempDir())
	src := t.TempDir()
	valid := filepath.Join(src, "直连.json")
	writeFile(t, valid, exampleConfig(t))
	invalid := filepath.Join(src, "broken.json")
	writeFile(t, invalid, []byte(`{"profiles":[]}`))

	entry, err := l.ImportConfig(valid, false)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "直连.json" || entry.Profile != "default" || entry.Entrance != "192.0.2.10:8964" || entry.UDP || entry.Err != nil {
		t.Fatalf("entry %+v", entry)
	}
	// A node that serves UDP adds a same-port UDP binding; the entrance is
	// still its TCP port.
	dual := filepath.Join(src, "双栈.json")
	writeFile(t, dual, []byte(strings.Replace(string(exampleConfig(t)), `"protocol": "TCP"
            }`, `"protocol": "TCP"
            },
            {
              "port": 8964,
              "protocol": "UDP"
            }`, 1)))
	if entry, err := l.ImportConfig(dual, false); err != nil || !entry.UDP || entry.Entrance != "192.0.2.10:8964" {
		t.Fatalf("dual-transport entry %+v %v", entry, err)
	}
	if err := l.DeleteConfig("双栈.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ImportConfig(invalid, false); err == nil {
		t.Fatal("an invalid configuration must not be imported")
	}
	if _, err := os.Stat(filepath.Join(l.Configs, "broken.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a rejected import must not leave a file behind")
	}

	// Same content again is fine; different content needs overwrite.
	if _, err := l.ImportConfig(valid, false); err != nil {
		t.Fatalf("re-importing identical content: %v", err)
	}
	changed := strings.Replace(string(exampleConfig(t)), `"mtu": 1400`, `"mtu": 1300`, 1)
	writeFile(t, valid, []byte(changed))
	if _, err := l.ImportConfig(valid, false); !errors.Is(err, ErrConfigExists) {
		t.Fatalf("different content without overwrite: %v", err)
	}
	if _, err := l.ImportConfig(valid, true); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	// A broken file placed in configs\ by hand is listed with its reason.
	writeFile(t, filepath.Join(l.Configs, "a-broken.json"), []byte("{"))
	writeFile(t, filepath.Join(l.Configs, "notes.txt"), []byte("ignored"))
	list, err := l.ListConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "a-broken.json" || list[0].Err == nil || list[1].Name != "直连.json" || list[1].Err != nil {
		t.Fatalf("list %+v", list)
	}
	if _, err := l.LoadConfig("直连.json"); err != nil {
		t.Fatal(err)
	}
	if err := l.DeleteConfig("直连.json"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`..\直连.json`, "../x.json", "x.txt", "", ".json", `C:\x.json`} {
		if _, err := l.ConfigPath(bad); err == nil {
			t.Errorf("config name %q accepted", bad)
		}
	}
}

func TestImportLegacyFolder(t *testing.T) {
	legacy := t.TempDir()
	cfg := exampleConfig(t)
	writeFile(t, filepath.Join(legacy, "直连.json"), cfg)
	writeFile(t, filepath.Join(legacy, "日本中转.json"), cfg)
	writeFile(t, filepath.Join(legacy, "bad.json"), []byte("{}"))
	writeFile(t, filepath.Join(legacy, "msboost-state.dat"), []byte("\xef\xbb\xbf"+`{"ConfigFile":"日本中转.json","Socks5Port":10086}`))

	l := NewLayout(t.TempDir())
	result, err := l.ImportLegacy(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Imported) != 2 || len(result.Skipped) != 1 || result.Skipped[0].Err == nil {
		t.Fatalf("result %+v", result)
	}
	if result.Current != "日本中转.json" {
		t.Fatalf("current %q", result.Current)
	}

	// The older single-line state file is honoured too.
	older := t.TempDir()
	writeFile(t, filepath.Join(older, "直连.json"), cfg)
	writeFile(t, filepath.Join(older, "msboost-current-config.txt"), []byte("直连.json\r\n"))
	result, err = NewLayout(t.TempDir()).ImportLegacy(older)
	if err != nil || result.Current != "直连.json" {
		t.Fatalf("older state file: %+v %v", result, err)
	}
}
