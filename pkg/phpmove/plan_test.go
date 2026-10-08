package phpmove

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, "composer.json", `{"autoload":{"psr-4":{"App\\":"src/"}},"autoload-dev":{"psr-4":{"Tests\\":["tests/"]}}}`)
	put(t, root, "src/Old/Foo.php", `<?php namespace App\Old;
final class Foo { public private(set) Peer $peer { get => $this->peer; } public function copy(): Foo { return new Foo; } }
`)
	put(t, root, "src/Old/Peer.php", `<?php namespace App\Old; class Peer { public Foo $foo; }`)
	put(t, root, "src/Client.php", `<?php namespace App; use App\Old\{Foo, Peer}; class Client { public Foo $foo; }`)
	put(t, root, "tests/Example.php", `<?php namespace Tests; use App\Old\Foo as Subject; class Example { public Subject $subject; }`)
	return root
}
func put(t *testing.T, root, name, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0640); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func plan(t *testing.T, root, from, to string) *Plan {
	t.Helper()
	p, err := Build(root, from, to)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanAndApplyDirectory(t *testing.T) {
	root := fixture(t)
	put(t, root, "src/Old/data.bin", string([]byte{0, 128, 255, 1}))
	if err := os.Mkdir(filepath.Join(root, "src/Old/empty"), 0750); err != nil {
		t.Fatal(err)
	}
	before := read(t, root, "src/Old/Foo.php")
	p := plan(t, root, "src/Old", "src/New")
	if len(p.Changes) != 5 || len(p.Classes) != 2 || len(p.Warnings) != 0 {
		t.Fatalf("unexpected plan: %+v", p)
	}
	if read(t, root, "src/Old/Foo.php") != before {
		t.Fatal("planning wrote source")
	}
	if _, err := os.Stat(filepath.Join(root, ".sf-move")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("planning created metadata")
	}
	encoded, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := Apply(decoded, false)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "applied" || receipt.Files != 5 {
		t.Fatalf("%+v", receipt)
	}
	if _, err := os.Stat(filepath.Join(root, "src/Old")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("old directory remains")
	}
	if got := read(t, root, "src/New/Foo.php"); !strings.Contains(got, `namespace App\New;`) || !strings.Contains(got, `Peer $peer { get`) {
		t.Fatal(got)
	}
	if got := read(t, root, "src/Client.php"); !strings.Contains(got, `use App\New\{Foo, Peer};`) {
		t.Fatal(got)
	}
	if got := read(t, root, "tests/Example.php"); !strings.Contains(got, `App\New\Foo as Subject`) {
		t.Fatal(got)
	}
	if got := read(t, root, "src/New/data.bin"); got != string([]byte{0, 128, 255, 1}) {
		t.Fatal("binary changed")
	}
	for name, mode := range map[string]fs.FileMode{"src/New/Foo.php": 0640, "src/New/empty": 0750} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("mode %s: %v %v", name, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(receipt.Transaction, "plan.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".sf-move/lock")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("lock remains")
	}
}

func TestFileRenamePreservesExternalAliasAndPeer(t *testing.T) {
	root := fixture(t)
	p := plan(t, root, "src/Old/Foo.php", "src/New/Renamed.php")
	if _, err := Apply(p, false); err != nil {
		t.Fatal(err)
	}
	got := read(t, root, "src/New/Renamed.php")
	for _, want := range []string{`class Renamed`, `\App\Old\Peer $peer`, `new \App\New\Renamed`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s: %s", want, got)
		}
	}
	if got := read(t, root, "src/Old/Peer.php"); !strings.Contains(got, `\App\New\Renamed $foo`) {
		t.Fatal(got)
	}
	if got := read(t, root, "src/Client.php"); !strings.Contains(got, `New\Renamed as Foo`) {
		t.Fatal(got)
	}
}

func TestStaleAndTamperedPlansNeverWriteSources(t *testing.T) {
	for _, change := range []string{"source", "consumer", "new_reference", "permissions", "edited_plan", "symlink", "empty_directory"} {
		t.Run(change, func(t *testing.T) {
			root := fixture(t)
			p := plan(t, root, "src/Old", "src/New")
			before := read(t, root, "src/Old/Foo.php")
			switch change {
			case "source":
				put(t, root, "src/Old/Foo.php", before+"\n")
				before += "\n"
			case "consumer":
				put(t, root, "src/Client.php", read(t, root, "src/Client.php")+"\n")
			case "new_reference":
				put(t, root, "src/Added.php", `<?php namespace App; class Added { public \App\Old\Foo $value; }`)
			case "permissions":
				if err := os.Chmod(filepath.Join(root, "src/Client.php"), 0600); err != nil {
					t.Fatal(err)
				}
			case "edited_plan":
				p.Changes[0].After = []byte("tampered")
			case "symlink":
				if err := os.Symlink("Client.php", filepath.Join(root, "src/New")); err != nil {
					t.Fatal(err)
				}
			case "empty_directory":
				if err := os.Mkdir(filepath.Join(root, "src/Old/added"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Apply(p, false); err == nil {
				t.Fatal("accepted stale plan")
			}
			if got := read(t, root, "src/Old/Foo.php"); got != before {
				t.Fatal("source changed")
			}
			if _, err := os.Stat(filepath.Join(root, "src/New/Foo.php")); !errors.Is(err, fs.ErrNotExist) && change != "symlink" {
				t.Fatalf("destination was created: %v", err)
			}
		})
	}
}

func TestRollbackAtEveryMutation(t *testing.T) {
	mutations := 0
	root := fixture(t)
	p := plan(t, root, "src/Old", "src/New")
	if _, err := apply(p, false, func(n int) error { mutations = n; return nil }); err != nil {
		t.Fatal(err)
	}
	for fail := 1; fail <= mutations; fail++ {
		t.Run(string(rune('A'+fail)), func(t *testing.T) {
			root := fixture(t)
			p := plan(t, root, "src/Old", "src/New")
			_, err := apply(p, false, func(n int) error {
				if n == fail {
					return errors.New("injected I/O failure")
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "was rolled back") {
				t.Fatal(err)
			}
			for _, c := range p.Changes {
				if got := read(t, root, c.From); got != string(c.Before) {
					t.Fatalf("not restored: %s", c.From)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "src/New")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("destination remains")
			}
		})
	}
}

func TestRollbackPreservesConcurrentEditAndRetainsLock(t *testing.T) {
	root := fixture(t)
	p := plan(t, root, "src/Old", "src/New")
	_, err := apply(p, false, func(_ int) error {
		name := filepath.Join(root, "src/New/Foo.php")
		if _, err := os.Stat(name); err == nil {
			put(t, root, "src/New/Foo.php", "external edit")
			return errors.New("injected failure")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "rollback incomplete") {
		t.Fatal(err)
	}
	if got := read(t, root, "src/New/Foo.php"); got != "external edit" {
		t.Fatal("external edit lost")
	}
	if _, err := os.Stat(filepath.Join(root, ".sf-move/lock")); err != nil {
		t.Fatal("recovery lock lost")
	}
	if _, err := Apply(p, false); err == nil {
		t.Fatal("accepted unfinished transaction")
	}
}

func TestInvalidMovesAndStrictCandidates(t *testing.T) {
	for _, kind := range []string{"overlap", "traversal", "absolute", "exists", "collision", "mismatch", "malformed_consumer", "symlink_source", "symlink_parent", "many_types", "function", "global", "ambiguous_mapping", "invalid_prefix", "wrong_extension"} {
		t.Run(kind, func(t *testing.T) {
			root := fixture(t)
			from, to := "src/Old/Foo.php", "src/New/Foo.php"
			switch kind {
			case "overlap":
				from, to = "src/Old", "src/Old/New"
			case "traversal":
				to = "../Foo.php"
			case "absolute":
				to = "/tmp/Foo.php"
			case "exists":
				to = "src/Old/Peer.php"
			case "collision":
				put(t, root, "src/Elsewhere.php", `<?php namespace App\New; class Foo {}`)
			case "mismatch":
				put(t, root, from, `<?php namespace Wrong; class Foo {}`)
			case "malformed_consumer":
				put(t, root, "src/Bad.php", `<?php namespace App; class Bad { public Foo $foo BROKEN`)
			case "symlink_source":
				if err := os.Symlink("Foo.php", filepath.Join(root, "src/Old/Link.php")); err != nil {
					t.Fatal(err)
				}
				from = "src/Old"
			case "symlink_parent":
				if err := os.Symlink("Old", filepath.Join(root, "src/New")); err != nil {
					t.Fatal(err)
				}
			case "many_types":
				put(t, root, from, `<?php namespace App\Old; class Foo {} class Other {}`)
			case "function":
				put(t, root, from, `<?php namespace App\Old; class Foo {} function helper() {}`)
			case "global":
				put(t, root, from, `<?php class Foo {}`)
			case "ambiguous_mapping":
				put(t, root, "composer.json", `{"autoload":{"psr-4":{"App\\":"src/","Other\\":"src/"}}}`)
			case "invalid_prefix":
				put(t, root, "composer.json", `{"autoload":{"psr-4":{"App":"src/"}}}`)
			case "wrong_extension":
				to = "src/New/Foo.PHP"
			}
			if _, err := Build(root, from, to); err == nil {
				t.Fatal("accepted invalid move")
			}
		})
	}
}

func TestWarningsAndUnrelatedBrokenPHP(t *testing.T) {
	root := fixture(t)
	put(t, root, "src/Broken.php", `<?php totally invalid syntax`)
	put(t, root, "src/Documentation.php", `<?php namespace App\Old; /** @var Foo */ class Documentation {}`)
	put(t, root, "config/services.yaml", `App\Old\Foo: ~`)
	p := plan(t, root, "src/Old/Foo.php", "src/New/Foo.php")
	if len(p.Warnings) != 2 {
		t.Fatalf("%+v", p.Warnings)
	}
	if _, err := Apply(p, false); err == nil {
		t.Fatal("warnings not gated")
	}
	if _, err := Apply(p, true); err != nil {
		t.Fatal(err)
	}
	if got := read(t, root, "config/services.yaml"); got != `App\Old\Foo: ~` {
		t.Fatal(got)
	}
}

func TestPlainFileAndEmptyDirectory(t *testing.T) {
	root := fixture(t)
	put(t, root, "assets/data.bin", string([]byte{0, 255, 128}))
	if _, err := Apply(plan(t, root, "assets/data.bin", "data/moved.bin"), false); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "assets/empty"), 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan(t, root, "assets/empty", "assets/moved"), false); err != nil {
		t.Fatal(err)
	}
}

func TestLimitsAndExclusionBoundaries(t *testing.T) {
	root := fixture(t)
	for _, name := range []string{"large-one.txt", "large-two.json"} {
		f, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(MaxFileBytes + 1); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Build(root, "src/Old", "src/New")
	if err == nil || !strings.Contains(err.Error(), "large-one.txt") || !strings.Contains(err.Error(), "large-two.json") {
		t.Fatal(err)
	}
	for _, skip := range []string{"src", "src/Old", "src/Old/Foo.php", "src/New", "composer.json", "../outside"} {
		if _, err := BuildExcluding(root, "src/Old", "src/New", []string{skip}); err == nil {
			t.Fatalf("accepted exclusion %s", skip)
		}
	}
	p, err := BuildExcluding(root, "src/Old", "src/New", []string{"large-one.txt", "large-two.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Excluded) != 2 {
		t.Fatal(p.Excluded)
	}
	if _, err := Apply(p, false); err != nil {
		t.Fatal(err)
	}
}
