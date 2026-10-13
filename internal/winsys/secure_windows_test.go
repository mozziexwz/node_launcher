package winsys

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestOpenLocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.dll")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := OpenLocked(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("evil"), 0o644); err == nil {
		t.Fatal("writing must fail while the file is locked")
	}
	if err := os.Rename(path, path+".old"); err == nil {
		t.Fatal("renaming must fail while the file is locked")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "data" {
		t.Fatalf("reading while locked: %q, %v", b, err)
	}
	f.Close()
	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := OpenLocked(path); err == nil {
		f.Close()
		t.Fatal("a file that is open for writing must not be lockable")
	}
	w.Close()

	if f, err := OpenLocked(dir); err == nil {
		f.Close()
		t.Fatal("a directory is not a regular file")
	}
}

func TestIsReparsePoint(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v %s", err, out)
	}
	if r, err := IsReparsePoint(link); err != nil || !r {
		t.Fatalf("junction: %v, %v", r, err)
	}
	if r, err := IsReparsePoint(target); err != nil || r {
		t.Fatalf("plain folder: %v, %v", r, err)
	}
	if _, err := IsReparsePoint(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing path: %v", err)
	}
}

func TestSecureDirectory(t *testing.T) {
	if !IsElevated() {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("the CI runner is expected to be elevated so that this test runs")
		}
		t.Skip("needs an elevated process")
	}
	dir := filepath.Join(t.TempDir(), "msboost")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SecureDirectory(dir); err != nil {
		t.Fatal(err)
	}
	admins, _ := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)

	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if owner, _, err := sd.Owner(); err != nil || !owner.Equals(admins) {
		t.Fatalf("owner %v, %v", owner, err)
	}
	if control, _, err := sd.Control(); err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("DACL must be protected from inheritance (control %#x, %v)", control, err)
	}
	checkGrants(t, sd, admins, system, false)

	// Files created inside inherit the same two grants and nothing else.
	file := filepath.Join(dir, "x.dll")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsd, err := windows.GetNamedSecurityInfo(file, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	checkGrants(t, fsd, admins, system, true)
}

func checkGrants(t *testing.T, sd *windows.SECURITY_DESCRIPTOR, admins, system *windows.SID, inherited bool) {
	t.Helper()
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("DACL: %v", err)
	}
	if dacl.AceCount != 2 {
		t.Fatalf("%d ACEs, want 2", dacl.AceCount)
	}
	seen := map[string]bool{}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != fileAllAccess {
			t.Fatalf("ACE %d: type %d mask %#x", i, ace.Header.AceType, ace.Mask)
		}
		if got := ace.Header.AceFlags&windows.INHERITED_ACE != 0; got != inherited {
			t.Fatalf("ACE %d inherited = %v, want %v", i, got, inherited)
		}
		switch {
		case sid.Equals(admins):
			seen["admins"] = true
		case sid.Equals(system):
			seen["system"] = true
		default:
			t.Fatalf("unexpected grant to %s", sid)
		}
	}
	if !seen["admins"] || !seen["system"] {
		t.Fatalf("grants %v", seen)
	}
}
