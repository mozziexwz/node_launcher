// Package runtimefiles releases the redirector's DLLs and driver, which
// release builds embed, into a folder that only administrators and SYSTEM
// can write, and checks them before every use (plan §6 M3 and §8).
//
// A portable exe often lives in a user-writable folder such as Downloads.
// Loading DLLs from beside it would let anything running as the user swap
// them and have them loaded with administrator rights.
package runtimefiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mozziexwz/node_launcher/internal/winsys"
)

// Manager owns one set of embedded files and their release folder.
type Manager struct {
	files  fs.FS
	names  []string
	sums   map[string][sha256.Size]byte
	base   string // %ProgramData%\MSBOOST
	digest string

	// secure locks a folder down; tests replace it when not elevated.
	secure func(dir string) error

	// Logf, if set, reports each file that had to be written.
	Logf func(format string, args ...any)
}

// New hashes the embedded files. base is the protected root folder; the
// files go to base\runtime\<digest>, so builds with identical files share
// one copy.
func New(files fs.FS, names []string, base string) (*Manager, error) {
	m := &Manager{files: files, names: names, sums: make(map[string][sha256.Size]byte), base: base,
		secure: winsys.SecureDirectory}
	all := sha256.New()
	for _, name := range names {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("程序不完整，缺少内嵌的运行文件 %s：%w", name, err)
		}
		sum := sha256.Sum256(data)
		m.sums[name] = sum
		fmt.Fprintf(all, "%s %x\n", name, sum)
	}
	m.digest = hex.EncodeToString(all.Sum(nil))[:16]
	return m, nil
}

// Dir is the folder the files are released to.
func (m *Manager) Dir() string { return filepath.Join(m.base, "runtime", m.digest) }

// Path is where the named file is released to.
func (m *Manager) Path(name string) string { return filepath.Join(m.Dir(), name) }

// Ensure secures the folders, makes every file present and intact, and
// returns with each file opened so that nobody can change or replace it
// until release is called. Call it right before loading the DLL or starting
// the driver, and release afterwards.
func (m *Manager) Ensure() (release func(), err error) {
	runtimeDir := filepath.Join(m.base, "runtime")
	for _, dir := range []string{m.base, runtimeDir, m.Dir()} {
		if err := m.prepareDir(dir); err != nil {
			return nil, err
		}
	}
	var locked []*os.File
	release = func() {
		for _, f := range locked {
			f.Close()
		}
	}
	for _, name := range m.names {
		f, err := m.ensureFile(name)
		if err != nil {
			release()
			return nil, err
		}
		locked = append(locked, f)
	}
	m.removeStale(runtimeDir)
	return release, nil
}

// prepareDir makes dir a real folder (a link could redirect the files to a
// writable place) and locks it down.
func (m *Manager) prepareDir(dir string) error {
	reparse, err := winsys.IsReparsePoint(dir)
	switch {
	case err == nil && reparse:
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("无法移除链接 %s：%w", dir, err)
		}
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("无法创建 %s：%w", dir, err)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s 不是文件夹", dir)
	}
	return m.secure(dir)
}

// ensureFile returns the named file opened and locked, after checking its
// hash; a missing or different file is replaced first.
func (m *Manager) ensureFile(name string) (*os.File, error) {
	path := m.Path(name)
	want := m.sums[name]
	if f := openVerified(path, want); f != nil {
		return f, nil
	}
	data, err := fs.ReadFile(m.files, name)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("运行文件 %s 已损坏，且正被占用无法替换：%w", name, err)
	}
	// O_EXCL never writes through a link someone placed at path.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("无法释放运行文件 %s：%w", name, err)
	}
	_, werr := io.Copy(f, bytes.NewReader(data))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, fmt.Errorf("无法释放运行文件 %s：%w", name, werr)
	}
	if f := openVerified(path, want); f != nil {
		if m.Logf != nil {
			m.Logf("已释放运行文件 %s", path)
		}
		return f, nil
	}
	return nil, fmt.Errorf("运行文件 %s 校验失败", name)
}

// openVerified opens path locked and returns it if its content hashes to
// want, nil otherwise.
func openVerified(path string, want [sha256.Size]byte) *os.File {
	f, err := winsys.OpenLocked(path)
	if err != nil {
		return nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil || !bytes.Equal(h.Sum(nil), want[:]) {
		f.Close()
		return nil
	}
	return f
}

// removeStale deletes the folders of other builds. Errors are ignored: a
// folder still in use is retried at the next start.
func (m *Manager) removeStale(runtimeDir string) {
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != m.digest {
			os.RemoveAll(filepath.Join(runtimeDir, e.Name()))
		}
	}
}

// Describe summarises the release folder for diagnostics.
func (m *Manager) Describe() string {
	return "内嵌，释放到 " + m.Dir()
}
