// Package store owns the portable data layout beside the executable:
//
//	configs\       node configurations downloaded from the website (*.json)
//	logs\          daily log files
//	data\          UI cache (WebView2)
//	settings.json  current config, probe target, addon switches
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mozziexwz/node_launcher/internal/engine"
	"github.com/mozziexwz/node_launcher/internal/probe"
	"github.com/mozziexwz/node_launcher/internal/profiles"
)

// Layout locates the portable data folders.
type Layout struct {
	Root     string
	Configs  string
	Logs     string
	Data     string
	Settings string
}

// NewLayout returns the layout rooted at dir.
func NewLayout(dir string) Layout {
	return Layout{
		Root:     dir,
		Configs:  filepath.Join(dir, "configs"),
		Logs:     filepath.Join(dir, "logs"),
		Data:     filepath.Join(dir, "data"),
		Settings: filepath.Join(dir, "settings.json"),
	}
}

// ExecutableLayout returns the layout beside the running executable.
func ExecutableLayout() (Layout, error) {
	exe, err := os.Executable()
	if err != nil {
		return Layout{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return NewLayout(filepath.Dir(exe)), nil
}

// Ensure creates the data folders.
func (l Layout) Ensure() error {
	for _, dir := range []string{l.Configs, l.Logs, l.Data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("无法创建数据目录 %s：%w", dir, err)
		}
	}
	return nil
}

// ---- settings ----

const settingsVersion = 1

// ProbeTarget is the game endpoint used by the full-path probe.
type ProbeTarget struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// Settings is persisted in settings.json.
type Settings struct {
	Version       int             `json:"version"`
	CurrentConfig string          `json:"currentConfig"`
	ProbeTarget   ProbeTarget     `json:"probeTarget"`
	Addons        map[string]bool `json:"addons"`
}

// DefaultSettings uses the default game's probe target and all addons off.
func DefaultSettings() Settings {
	game := profiles.DefaultMapleStory()
	s := Settings{
		Version:     settingsVersion,
		ProbeTarget: ProbeTarget{Address: game.TestAddress, Port: game.TestPort},
		Addons:      make(map[string]bool),
	}
	for _, a := range profiles.Addons() {
		s.Addons[a.ID] = false
	}
	return s
}

// Validate checks values a user can edit.
func (s Settings) Validate() error {
	game := profiles.DefaultMapleStory()
	game.TestAddress, game.TestPort = s.ProbeTarget.Address, s.ProbeTarget.Port
	if err := game.Validate(); err != nil {
		return err
	}
	known := make(map[string]bool)
	for _, a := range profiles.Addons() {
		known[a.ID] = true
	}
	for id := range s.Addons {
		if !known[id] {
			return fmt.Errorf("未知的可选加速项：%q", id)
		}
	}
	if s.CurrentConfig != "" {
		if err := validConfigName(s.CurrentConfig); err != nil {
			return err
		}
	}
	return nil
}

// LoadSettings reads settings.json. A missing file yields the defaults; an
// unreadable or invalid file also yields the defaults plus the reason, so
// the caller can tell the user without refusing to start.
func (l Layout) LoadSettings() (Settings, error) {
	defaults := DefaultSettings()
	b, err := os.ReadFile(l.Settings)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, fmt.Errorf("读取设置失败，已使用默认设置：%w", err)
	}
	s := DefaultSettings()
	if err := json.Unmarshal(b, &s); err != nil {
		return defaults, fmt.Errorf("设置文件已损坏，已使用默认设置：%w", err)
	}
	if s.Addons == nil {
		s.Addons = defaults.Addons
	}
	for id := range defaults.Addons {
		if _, ok := s.Addons[id]; !ok {
			s.Addons[id] = false
		}
	}
	if err := s.Validate(); err != nil {
		return defaults, fmt.Errorf("设置无效，已使用默认设置：%w", err)
	}
	s.Version = settingsVersion
	return s, nil
}

// SaveSettings validates and writes settings.json atomically.
func (l Layout) SaveSettings(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	s.Version = settingsVersion
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(l.Settings, append(b, '\n'))
}

// ---- node configurations ----

// ConfigEntry describes one file in configs\. Passwords are never exposed.
type ConfigEntry struct {
	Name     string // file name, e.g. 直连.json
	Path     string
	Profile  string // active profile name
	Entrance string // "address:port", or the reason no single entrance exists
	Err      error  // set when the file is not a valid configuration
}

// ErrConfigExists means an import would replace a different file.
var ErrConfigExists = errors.New("配置文件已存在")

// ListConfigs returns every *.json in configs\, sorted by name, each
// validated with the core's own parser.
func (l Layout) ListConfigs() ([]ConfigEntry, error) {
	items, err := os.ReadDir(l.Configs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []ConfigEntry
	for _, item := range items {
		if item.IsDir() || !strings.EqualFold(filepath.Ext(item.Name()), ".json") {
			continue
		}
		entries = append(entries, describeConfig(item.Name(), filepath.Join(l.Configs, item.Name())))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// LoadConfig parses a configuration from configs\ by file name.
func (l Layout) LoadConfig(name string) (*engine.Config, error) {
	path, err := l.ConfigPath(name)
	if err != nil {
		return nil, err
	}
	return engine.LoadConfigFile(path)
}

// ConfigPath maps a file name to its path inside configs\.
func (l Layout) ConfigPath(name string) (string, error) {
	if err := validConfigName(name); err != nil {
		return "", err
	}
	return filepath.Join(l.Configs, name), nil
}

// ImportConfig validates src and copies it into configs\. An existing file
// with identical content is accepted; different content needs overwrite.
func (l Layout) ImportConfig(src string, overwrite bool) (ConfigEntry, error) {
	name := filepath.Base(src)
	dst, err := l.ConfigPath(name)
	if err != nil {
		return ConfigEntry{}, err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return ConfigEntry{}, fmt.Errorf("读取配置文件失败：%w", err)
	}
	if _, err := engine.ParseConfig(b); err != nil {
		return ConfigEntry{}, err
	}
	if same, err := sameAbsPath(src, dst); err == nil && same {
		return describeConfig(name, dst), nil
	}
	if existing, err := os.ReadFile(dst); err == nil && !overwrite && string(existing) != string(b) {
		return ConfigEntry{}, fmt.Errorf("%w：%s", ErrConfigExists, name)
	}
	if err := os.MkdirAll(l.Configs, 0o755); err != nil {
		return ConfigEntry{}, err
	}
	if err := writeFileAtomic(dst, b); err != nil {
		return ConfigEntry{}, err
	}
	return describeConfig(name, dst), nil
}

// DeleteConfig removes a configuration file from configs\.
func (l Layout) DeleteConfig(name string) error {
	path, err := l.ConfigPath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// LegacyImport is the result of importing an old BAT launcher folder.
type LegacyImport struct {
	Imported []ConfigEntry
	Skipped  []ConfigEntry // invalid configurations, with reasons
	Current  string        // the config the old launcher last loaded, if imported
}

// ImportLegacy imports the valid *.json configurations next to the old BAT
// launcher and reads which one it last loaded (msboost-state.dat, or the
// older msboost-current-config.txt).
func (l Layout) ImportLegacy(dir string) (LegacyImport, error) {
	var result LegacyImport
	items, err := os.ReadDir(dir)
	if err != nil {
		return result, fmt.Errorf("无法读取旧版目录：%w", err)
	}
	for _, item := range items {
		if item.IsDir() || !strings.EqualFold(filepath.Ext(item.Name()), ".json") {
			continue
		}
		src := filepath.Join(dir, item.Name())
		entry, err := l.ImportConfig(src, false)
		if err != nil {
			skipped := describeConfig(item.Name(), src)
			if skipped.Err == nil {
				skipped.Err = err
			}
			result.Skipped = append(result.Skipped, skipped)
			continue
		}
		result.Imported = append(result.Imported, entry)
	}
	last := legacyCurrentConfig(dir)
	for _, e := range result.Imported {
		if e.Name == last {
			result.Current = last
		}
	}
	return result, nil
}

func legacyCurrentConfig(dir string) string {
	if b, err := os.ReadFile(filepath.Join(dir, "msboost-state.dat")); err == nil {
		var state struct{ ConfigFile string }
		if json.Unmarshal(trimBOM(b), &state) == nil && state.ConfigFile != "" {
			return state.ConfigFile
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "msboost-current-config.txt")); err == nil {
		return strings.TrimSpace(string(trimBOM(b)))
	}
	return ""
}

func describeConfig(name, path string) ConfigEntry {
	entry := ConfigEntry{Name: name, Path: path}
	cfg, err := engine.LoadConfigFile(path)
	if err != nil {
		entry.Err = err
		return entry
	}
	entry.Profile = cfg.Profile.GetProfileName()
	if t := probe.RelayTargetFromProfile(cfg.Profile); t.Err == "" {
		entry.Entrance = fmt.Sprintf("%s:%d", t.Address, t.Port)
	} else {
		entry.Entrance = t.Err
	}
	return entry
}

func validConfigName(name string) error {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `\/:*?"<>|`) ||
		strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".json") {
		return fmt.Errorf("配置文件名无效：%q", name)
	}
	return nil
}

func sameAbsPath(a, b string) (bool, error) {
	pa, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	pb, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(filepath.Clean(pa), filepath.Clean(pb)), nil
}

func trimBOM(b []byte) []byte {
	return []byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf"))
}

// writeFileAtomic writes to a temporary file in the same directory and
// renames it over the destination, so a crash never leaves a torn file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}
