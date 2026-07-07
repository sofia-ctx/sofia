package doctor

import (
	"testing"
	"time"
)

func TestClassifyStaleness(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		bin, head time.Time
		dirtyGo   bool
		want      string
	}{
		{"head newer than build → fail", base, base.Add(time.Hour), false, statusFail},
		{"build newer, clean → ok", base.Add(time.Hour), base, false, statusOK},
		{"build newer, uncommitted go → warn", base.Add(time.Hour), base, true, statusWarn},
		{"equal, clean → ok", base, base, false, statusOK},
		{"equal, dirty go → warn", base, base, true, statusWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, detail := classifyStaleness(tc.bin, tc.head, tc.dirtyGo)
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
			if detail == "" {
				t.Fatal("detail must not be empty")
			}
		})
	}
}

func TestCompareSkill(t *testing.T) {
	same := []byte("# sf-context\n")
	other := []byte("# sf-context (edited)\n")

	if status, _ := compareSkill(same, same); status != statusOK {
		t.Errorf("identical → status = %q, want %q", status, statusOK)
	}
	status, detail := compareSkill(other, same)
	if status != statusWarn {
		t.Errorf("differing → status = %q, want %q", status, statusWarn)
	}
	if detail == "" {
		t.Error("detail must not be empty")
	}
}

func TestPorcelainHasGo(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"modified go", " M internal/common/doctor/doctor.go\n", true},
		{"staged go", "A  cmd/common/doctor/main.go\n", true},
		{"untracked go", "?? foo/bar.go\n", true},
		{"rename to go", "R  old.txt -> new.go\n", true},
		{"rename from go to txt", "R  old.go -> new.txt\n", false},
		{"only docs/config", " M README.md\n M go.mod\n?? notes.txt\n", false},
		{"empty", "", false},
		{"short line ignored", "M\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := porcelainHasGo(tc.out); got != tc.want {
				t.Fatalf("porcelainHasGo(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}
