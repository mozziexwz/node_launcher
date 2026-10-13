// Command notices writes THIRD_PARTY_NOTICES.txt for the release zip: the
// copyright and license texts of everything compiled into
// MSBOOST-Launcher.exe. Run it from the repository root after
// fetch-windivert.ps1 and the frontend install:
//
//	go run ./scripts/notices -o build/release/THIRD_PARTY_NOTICES.txt
//
// Go modules come from `go list -deps`, so the list follows go.mod. Texts
// identical to LICENSE (GPL-3.0) point there instead of being repeated, and
// components sharing a text are listed together.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type component struct {
	name, version, url string
	files              []string // license, notice and patent files
}

func main() {
	out := flag.String("o", "THIRD_PARTY_NOTICES.txt", "output file")
	windivert := flag.String("windivert", ".cache/windivert/WinDivert-2.2.2-A/LICENSE", "WinDivert license file")
	nodeModules := flag.String("node", "frontend/node_modules", "frontend node_modules folder")
	flag.Parse()

	comps, err := goModules()
	if err != nil {
		log.Fatal(err)
	}
	goroot, err := goEnv("GOROOT")
	if err != nil {
		log.Fatal(err)
	}
	comps = append(comps,
		component{name: "Go", version: "runtime and standard library", url: "https://go.dev",
			files: []string{filepath.Join(goroot, "LICENSE"), filepath.Join(goroot, "PATENTS")}},
		component{name: "ProxyBridge", version: "4.1.0 (with MSBOOST patches)", url: "https://github.com/InterceptSuite/ProxyBridge",
			files: []string{"third_party/ProxyBridge/LICENSE"}},
		component{name: "WinDivert", version: "2.2.2-A", url: "https://github.com/basil00/WinDivert",
			files: []string{*windivert}},
	)
	for _, pkg := range []string{"react", "react-dom", "scheduler"} {
		version, err := npmVersion(filepath.Join(*nodeModules, pkg, "package.json"))
		if err != nil {
			log.Fatal(err)
		}
		comps = append(comps, component{name: pkg, version: version, url: "https://react.dev",
			files: []string{filepath.Join(*nodeModules, pkg, "LICENSE")}})
	}

	gpl, err := os.ReadFile("LICENSE")
	if err != nil {
		log.Fatal(err)
	}
	text, err := render(comps, normalize(gpl))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	// CRLF so that every Notepad shows the lines.
	if err := os.WriteFile(*out, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s: %d components\n", *out, len(comps))
}

// goModules lists the modules whose packages are linked into the launcher.
func goModules() ([]component, error) {
	cmd := exec.Command("go", "list", "-deps", "-json=Module", ".")
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	seen := map[string]bool{}
	var comps []component
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		var pkg struct {
			Module *struct {
				Path, Version, Dir string
				Main               bool
				Replace            *struct{ Path, Version, Dir string }
			}
		}
		if err := dec.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		m := pkg.Module
		if m == nil || m.Main || seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		version, dir := m.Version, m.Dir
		if m.Replace != nil {
			version, dir = m.Replace.Version, m.Replace.Dir
		}
		files, err := licenseFiles(dir)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%s: no license file in %s", m.Path, dir)
		}
		comps = append(comps, component{name: m.Path, version: version, url: "https://pkg.go.dev/" + m.Path, files: files})
	}
	sort.Slice(comps, func(i, j int) bool { return comps[i].name < comps[j].name })
	return comps, nil
}

var licenseName = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)(\.(txt|md))?$`)

func licenseFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && licenseName.MatchString(e.Name()) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func goEnv(name string) (string, error) {
	out, err := exec.Command("go", "env", name).Output()
	return strings.TrimSpace(string(out)), err
}

func npmVersion(packageJSON string) (string, error) {
	raw, err := os.ReadFile(packageJSON)
	if err != nil {
		return "", err
	}
	var p struct{ Version string }
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("%s: %w", packageJSON, err)
	}
	return p.Version, nil
}

func normalize(b []byte) string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return strings.TrimSpace(strings.TrimPrefix(s, "\xef\xbb\xbf")) + "\n"
}

// licenseKind names a text for the summary; the full text always follows.
func licenseKind(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	switch {
	case strings.Contains(t, "GNU LESSER GENERAL PUBLIC LICENSE"):
		return "LGPL-3.0"
	case strings.Contains(t, "GNU GENERAL PUBLIC LICENSE Version 3"):
		return "GPL-3.0"
	case strings.Contains(t, "Apache License Version 2.0"), strings.Contains(t, "Apache License, Version 2.0"):
		return "Apache-2.0"
	case strings.Contains(t, "Permission is hereby granted, free of charge"):
		return "MIT"
	case strings.Contains(t, "Redistribution and use in source and binary forms"):
		if strings.Contains(t, "Neither the name") {
			return "BSD-3-Clause"
		}
		return "BSD-2-Clause"
	}
	return "see text"
}

func render(comps []component, gpl string) (string, error) {
	type block struct {
		names []string
		text  string
	}
	var blocks []*block
	byHash := map[[32]byte]*block{}
	var summary strings.Builder
	for _, c := range comps {
		var kinds []string
		for _, f := range c.files {
			raw, err := os.ReadFile(f)
			if err != nil {
				return "", fmt.Errorf("%s: %w", c.name, err)
			}
			text := normalize(raw)
			base := strings.ToUpper(filepath.Base(f))
			if strings.HasPrefix(base, "LICEN") || strings.HasPrefix(base, "COPYING") {
				if text == gpl {
					kinds = append(kinds, "GPL-3.0 (LICENSE.txt)")
					continue
				}
				kinds = append(kinds, licenseKind(text))
			}
			label := fmt.Sprintf("%s %s — %s", c.name, c.version, filepath.Base(f))
			key := sha256.Sum256([]byte(text))
			if b, ok := byHash[key]; ok {
				b.names = append(b.names, label)
				continue
			}
			b := &block{names: []string{label}, text: text}
			byHash[key] = b
			blocks = append(blocks, b)
		}
		fmt.Fprintf(&summary, "- %s %s  [%s]  %s\n", c.name, c.version, strings.Join(kinds, ", "), c.url)
	}

	var sb strings.Builder
	sb.WriteString("MSBOOST 启动器 — 第三方软件声明 / Third-party notices\n")
	sb.WriteString(strings.Repeat("=", 78) + "\n\n")
	sb.WriteString("MSBOOST 启动器依据 GNU 通用公共许可证第 3 版（GPL-3.0）发布，许可全文见 LICENSE.txt。\n")
	sb.WriteString("本程序包含以下第三方软件，它们的版权声明与许可文本附在后面。\n\n")
	sb.WriteString("MSBOOST Launcher is licensed under the GNU General Public License v3.0 (LICENSE.txt).\n")
	sb.WriteString("It includes the third-party software listed below; their notices follow.\n\n")
	sb.WriteString(summary.String())
	for _, b := range blocks {
		sb.WriteString("\n" + strings.Repeat("=", 78) + "\n")
		for _, n := range b.names {
			sb.WriteString(n + "\n")
		}
		sb.WriteString(strings.Repeat("-", 78) + "\n\n")
		sb.WriteString(b.text)
	}
	return sb.String(), nil
}
