package runtimefiles

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/fstest"
)

var testFiles = fstest.MapFS{
	"core.dll":   {Data: []byte("core v1")},
	"divert.dll": {Data: []byte("divert v1")},
	"divert.sys": {Data: []byte("driver v1")},
}

var testNames = []string{"core.dll", "divert.dll", "divert.sys"}

// newTestManager uses a temporary base folder. Locking folders down needs
// elevation and is covered by winsys.TestSecureDirectory.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(testFiles, testNames, filepath.Join(t.TempDir(), "MSBOOST"))
	if err != nil {
		t.Fatal(err)
	}
	m.secure = func(string) error { return nil }
	return m
}

func ensure(t *testing.T, m *Manager) func() {
	t.Helper()
	release, err := m.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func checkContents(t *testing.T, m *Manager) {
	t.Helper()
	for _, name := range testNames {
		got, err := os.ReadFile(m.Path(name))
		if err != nil || string(got) != string(testFiles[name].Data) {
			t.Fatalf("%s: %q, %v", name, got, err)
		}
	}
}

func TestEnsureReleasesAndLocks(t *testing.T) {
	m := newTestManager(t)
	var secured []string
	m.secure = func(dir string) error {
		secured = append(secured, dir)
		return nil
	}
	release := ensure(t, m)
	checkContents(t, m)
	want := []string{m.base, filepath.Join(m.base, "runtime"), m.Dir()}
	if len(secured) != 3 || secured[0] != want[0] || secured[1] != want[1] || secured[2] != want[2] {
		t.Fatalf("secured %v, want %v", secured, want)
	}

	// While locked, the files can be read but not changed or replaced.
	if err := os.WriteFile(m.Path("divert.sys"), []byte("evil"), 0o644); err == nil {
		t.Fatal("the driver must not be writable while locked")
	}
	if err := os.Rename(m.Path("core.dll"), m.Path("core.old")); err == nil {
		t.Fatal("the DLL must not be replaceable while locked")
	}
	release()
	if err := os.WriteFile(m.Path("divert.sys"), []byte("evil"), 0o644); err != nil {
		t.Fatalf("after release the file is ordinary again: %v", err)
	}
}

func TestEnsureRepairsFiles(t *testing.T) {
	m := newTestManager(t)
	ensure(t, m)()
	if err := os.WriteFile(m.Path("core.dll"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.Path("divert.dll")); err != nil {
		t.Fatal(err)
	}
	ensure(t, m)()
	checkContents(t, m)
}

func TestEnsureFailsWhileSomeoneWrites(t *testing.T) {
	m := newTestManager(t)
	ensure(t, m)()
	w, err := os.OpenFile(m.Path("divert.sys"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if release, err := m.Ensure(); err == nil {
		release()
		t.Fatal("a file held open for writing by someone else must fail the check")
	}
}

func TestEnsureReplacesLinkedFolder(t *testing.T) {
	m := newTestManager(t)
	if err := os.MkdirAll(filepath.Join(m.base, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", m.Dir(), elsewhere).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v %s", err, out)
	}
	ensure(t, m)()
	if info, err := os.Lstat(m.Dir()); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("release folder must be a real folder: %v, %v", info, err)
	}
	checkContents(t, m)
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 1 {
		t.Fatalf("the link target must be left alone, has %d entries", len(entries))
	}
}

func TestEnsureRemovesOtherBuilds(t *testing.T) {
	m := newTestManager(t)
	old := filepath.Join(m.base, "runtime", "0123456789abcdef")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "core.dll"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensure(t, m)()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("the old build's folder must be removed: %v", err)
	}
}

func TestDigestFollowsContent(t *testing.T) {
	a, err := New(testFiles, testNames, `C:\x`)
	if err != nil {
		t.Fatal(err)
	}
	changed := fstest.MapFS{"core.dll": {Data: []byte("core v2")}, "divert.dll": testFiles["divert.dll"], "divert.sys": testFiles["divert.sys"]}
	b, err := New(changed, testNames, `C:\x`)
	if err != nil {
		t.Fatal(err)
	}
	if a.Dir() == b.Dir() || len(a.digest) != 16 {
		t.Fatalf("digests %q and %q", a.digest, b.digest)
	}
	if _, err := New(testFiles, []string{"missing.dll"}, `C:\x`); err == nil {
		t.Fatal("a missing embedded file must be an error")
	}
}
