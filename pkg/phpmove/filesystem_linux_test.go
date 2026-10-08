package phpmove

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRejectCrossFilesystemBeforeSourceWrites(t *testing.T) {
	base, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.Stat("/dev/shm")
	if err != nil {
		t.Skip("no second filesystem")
	}
	if base.Sys().(*syscall.Stat_t).Dev == other.Sys().(*syscall.Stat_t).Dev {
		t.Skip("no second filesystem")
	}
	name := filepath.Join(t.TempDir(), "source.php")
	if err := os.WriteFile(name, []byte("source unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot("/")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p := &Plan{Changes: []Change{{From: strings.TrimPrefix(name, "/"), To: "dev/shm/new-parent/file.php"}}}
	if err := validateFilesystem(r, p); err == nil || !strings.Contains(err.Error(), "cross-filesystem") {
		t.Fatal(err)
	}
	got, err := os.ReadFile(name)
	if err != nil || string(got) != "source unchanged" {
		t.Fatalf("source changed: %q %v", got, err)
	}
}
