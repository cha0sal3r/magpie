package shortcut

import "testing"

func TestTarget(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Tools\magpie-friday-windows-amd64.exe`:          `C:\Tools\magpie-friday-windows-amd64.exe`,
		`C:\Tools\magpie-friday.exe.old`:                    `C:\Tools\magpie-friday.exe`,
		`C:\Tools\magpie-friday.exe.old-3`:                  `C:\Tools\magpie-friday.exe`,
		`C:\Tools\magpie-friday.exe.new`:                    `C:\Tools\magpie-friday.exe`,
		`C:\Tools\MAGPIE.EXE.OLD`:                    `C:\Tools\MAGPIE.EXE`,
		`C:\old\magpie-friday.exe`:                          `C:\old\magpie-friday.exe`,
		`C:\Tools\magpie-friday.exe.older`:                  `C:\Tools\magpie-friday.exe.older`,
		`/usr/local/bin/magpie-friday`:                      `/usr/local/bin/magpie-friday`,
		`C:\Users\me\Downloads\magpie-friday (1).exe.old-2`: `C:\Users\me\Downloads\magpie-friday (1).exe`,
	} {
		if got := Target(in); got != want {
			t.Errorf("Target(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStale(t *testing.T) {
	exe := "/tools/magpie-friday.exe"
	for _, c := range []struct {
		target, dir string
		stale       bool
	}{
		{"", "", true},                            // no shortcut yet
		{"/tools/magpie-friday.exe", "/tools", false},    // already this one
		{"/TOOLS/magpie-friday.EXE", "/Tools/", false},   // the same, in another case
		{"/downloads/magpie-friday.exe", "/tools", true}, // the exe was moved
		{"/tools/magpie-friday.exe", "/downloads", true}, // starts in another folder
		{"/tools/magpie-friday.exe", "", true},           // in no folder
	} {
		if got := Stale(c.target, c.dir, exe); got != c.stale {
			t.Errorf("Stale(%q, %q) = %v, want %v", c.target, c.dir, got, c.stale)
		}
	}
}
