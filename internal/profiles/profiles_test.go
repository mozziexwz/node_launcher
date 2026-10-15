package profiles

import (
	"slices"
	"strings"
	"testing"
)

func TestDefaultMapleStory(t *testing.T) {
	g := DefaultMapleStory()
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(g.Processes) != 31 {
		t.Fatalf("got %d processes, want the 24 tested entries plus 7 added in v1.0.2", len(g.Processes))
	}
	// Without ms-launcher.exe the Steam version's login window stays black.
	for _, p := range []string{"ms-launcher.exe", "DwarfAxe.exe", "MapleBrowser_WZ2.exe",
		"grap-core64.aes", "grap-communicator64.aes", "grap-updater.aes", "NGService.exe"} {
		if !slices.Contains(g.Processes, p) {
			t.Errorf("%s missing from the list", p)
		}
	}
	list := g.ProcessList()
	if strings.HasSuffix(list, ";") || !strings.Contains(list, "Kaizen v92.exe;Kaizenv92.exe") {
		t.Fatalf("unexpected list %q", list)
	}
	if g.Protocol != ProtocolBoth || !g.SendDomainToProxy {
		t.Fatal("default must match the tested profile: BOTH and SendDomainToProxy")
	}
}

func TestDisplayGroupsCoverTheListExactlyOnce(t *testing.T) {
	var grouped []string
	for _, g := range DisplayGroups() {
		if g.Name == "" || len(g.Processes) == 0 {
			t.Fatalf("empty group %+v", g)
		}
		grouped = append(grouped, g.Processes...)
	}
	want := DefaultMapleStory().Processes
	if strings.Join(grouped, ";") != strings.Join(want, ";") {
		t.Fatalf("groups must list every process once, in order:\n%v\n%v", grouped, want)
	}
}

func TestAddons(t *testing.T) {
	game := make(map[string]bool)
	for _, p := range DefaultMapleStory().Processes {
		game[strings.ToLower(p)] = true
	}
	ids := make(map[string]bool)
	for _, a := range Addons() {
		if a.ID == "" || ids[a.ID] {
			t.Fatalf("addon id %q empty or duplicated", a.ID)
		}
		ids[a.ID] = true
		if len(a.Processes) == 0 || a.Protocol != ProtocolBoth {
			t.Fatalf("addon %s: unexpected definition %+v", a.ID, a)
		}
		for _, p := range a.Processes {
			if err := ValidateProcessName(p); err != nil {
				t.Fatalf("addon %s: %v", a.ID, err)
			}
			if game[strings.ToLower(p)] {
				t.Fatalf("addon %s overlaps the game list with %s", a.ID, p)
			}
		}
	}
	if !ids["discord"] || !ids["steam"] {
		t.Fatal("Discord and Steam addons are required")
	}
}

func TestValidateProcessName(t *testing.T) {
	for _, ok := range []string{"MapleStory.exe", "Kaizen v92.exe", "Maple*.exe", "BlackCipher64.aes"} {
		if err := ValidateProcessName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", " ", " a.exe", "a.exe;b.exe", "*", "**"} {
		if err := ValidateProcessName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidateTestTarget(t *testing.T) {
	g := DefaultMapleStory()
	for _, addr := range []string{"10.0.0.1", "127.0.0.1", "035.155.204.207", "::1", "example.com"} {
		g.TestAddress = addr
		if err := g.Validate(); err == nil {
			t.Errorf("test address %q accepted", addr)
		}
	}
}
