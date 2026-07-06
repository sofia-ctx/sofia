package dedup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeLine hand-writes one record into a session's dedup file, bypassing
// Guard — for tests that need a specific ts/n the normal Begin/Commit flow
// wouldn't produce (an old timestamp, a future one, …).
func writeLine(t *testing.T, sid string, r record) {
	t.Helper()
	path := statePath(sid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestWindow(t *testing.T) {
	cases := []struct {
		name string
		val  string
		want time.Duration
	}{
		{"unset defaults to 180s", "", DefaultWindow},
		{"0 disables it", "0", 0},
		{"explicit seconds", "45", 45 * time.Second},
		{"garbage falls back to default", "banana", DefaultWindow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SOFIA_DEDUP_WINDOW", tc.val)
			if got := Window(); got != tc.want {
				t.Errorf("Window() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestKeyIncludesTool(t *testing.T) {
	if Key("code", "x=1") == Key("grep", "x=1") {
		t.Error("different tools must not share a key")
	}
}

func TestKeyOrderInsensitive(t *testing.T) {
	a := Key("code", "a=1", "b=2")
	b := Key("code", "b=2", "a=1")
	if a != b {
		t.Errorf("Key not order-insensitive: %q vs %q", a, b)
	}
}

func TestBeginDisabledWithoutSession(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	g := Begin("code", false, "x=1")
	if h := g.Hit(); h != nil {
		t.Fatalf("expected nil Hit without a session, got %+v", h)
	}
	g.CommitFull(100) // must be a no-op — nothing to key the file on

	g2 := Begin("code", false, "x=1")
	if h := g2.Hit(); h != nil {
		t.Fatalf("dedup must stay fully off without a session, got %+v", h)
	}
}

func TestBeginDisabledUnderTestWithoutWindowEnv(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("SOFIA_SESSION_ID", "sid-under-test")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "") // NOT set explicitly

	g := Begin("code", false, "x=1")
	g.CommitFull(100)

	g2 := Begin("code", false, "x=1")
	if h := g2.Hit(); h != nil {
		t.Fatalf("under test without SOFIA_DEDUP_WINDOW set, dedup must stay disabled; got %+v", h)
	}
}

func TestHitWithinWindow(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("SOFIA_SESSION_ID", "sid-1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	g := Begin("code", false, "x=1")
	if g.Hit() != nil {
		t.Fatal("first call must not be a hit")
	}
	g.CommitFull(500)

	g2 := Begin("code", false, "x=1")
	h := g2.Hit()
	if h == nil {
		t.Fatal("second identical call within window must hit")
	}
	if h.N != 1 || h.Tok != 500 {
		t.Errorf("hit = %+v, want N=1 Tok=500", h)
	}
	if h.Age < 0 || h.Age > 2*time.Second {
		t.Errorf("age = %v, want ~0", h.Age)
	}
}

func TestMissAfterWindow(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("SOFIA_SESSION_ID", "sid-1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	key := Key("code", "x=1")
	writeLine(t, "sid-1", record{FP: key, N: 1, TS: time.Now().Unix() - 400, Tok: 100})

	g := Begin("code", false, "x=1")
	if h := g.Hit(); h != nil {
		t.Fatalf("expected a miss once the window elapsed, got %+v", h)
	}
}

func TestForceBypassesButRecords(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("SOFIA_SESSION_ID", "sid-1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	g1 := Begin("code", false, "x=1")
	g1.CommitFull(100) // N=1

	g2 := Begin("code", true, "x=1") // force
	if h := g2.Hit(); h != nil {
		t.Fatalf("force must bypass the hit, got %+v", h)
	}
	g2.CommitFull(200) // N=2 — the force call still records itself

	g3 := Begin("code", false, "x=1")
	h := g3.Hit()
	if h == nil {
		t.Fatal("expected a hit after the force call recorded itself")
	}
	if h.N != 2 {
		t.Errorf("hit.N = %d, want 2 (the force call's own ordinal)", h.N)
	}
}

func TestStubCommitSlidesWindow(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("SOFIA_SESSION_ID", "sid-1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	g1 := Begin("code", false, "x=1")
	g1.CommitFull(100) // N=1

	g2 := Begin("code", false, "x=1")
	h2 := g2.Hit()
	if h2 == nil || h2.N != 1 {
		t.Fatalf("expected hit N=1, got %+v", h2)
	}
	g2.CommitStub()

	g3 := Begin("code", false, "x=1")
	h3 := g3.Hit()
	if h3 == nil {
		t.Fatal("expected a hit after CommitStub")
	}
	if h3.N != 1 {
		t.Errorf("hit.N = %d, want 1 (the ORIGINAL call, not the stub)", h3.N)
	}
}

func TestDifferentSessionsIsolated(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	t.Setenv("SOFIA_SESSION_ID", "sid-a")
	ga := Begin("code", false, "x=1")
	ga.CommitFull(100)

	t.Setenv("SOFIA_SESSION_ID", "sid-b")
	gb := Begin("code", false, "x=1")
	if h := gb.Hit(); h != nil {
		t.Fatalf("a different session must not see sid-a's dedup state, got %+v", h)
	}
}

func TestCorruptLineIgnored(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("SOFIA_SESSION_ID", "sid-1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	key := Key("code", "x=1")
	path := statePath("sid-1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	good, err := json.Marshal(record{FP: key, N: 1, TS: time.Now().Unix(), Tok: 100})
	if err != nil {
		t.Fatal(err)
	}
	// A malformed line ahead of a valid one must be skipped, not abort the
	// scan.
	content := "{not json\n" + string(good) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	g := Begin("code", false, "x=1")
	h := g.Hit()
	if h == nil || h.N != 1 {
		t.Fatalf("expected the valid line to still produce a hit, got %+v", h)
	}
}

func TestFutureTimestampMiss(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("SOFIA_SESSION_ID", "sid-1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	key := Key("code", "x=1")
	writeLine(t, "sid-1", record{FP: key, N: 1, TS: time.Now().Unix() + 500, Tok: 100})

	g := Begin("code", false, "x=1")
	if h := g.Hit(); h != nil {
		t.Fatalf("a future timestamp (clock skew) must be a miss, got %+v", h)
	}
}

func TestGCRemovesStaleSessions(t *testing.T) {
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("SOFIA_SESSION_ID", "sid-fresh")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	staleSid := "sid-stale"
	writeLine(t, staleSid, record{FP: "x", N: 1, TS: time.Now().Unix(), Tok: 1})
	stalePath := statePath(staleSid)
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatal(err)
	}

	g := Begin("code", false, "x=1")
	g.CommitFull(100) // gc runs as a side effect of the write

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("stale session file should have been gc'd, stat err=%v", err)
	}
}

func TestSanitizeSidNoPathEscape(t *testing.T) {
	got := sanitize("../../etc/passwd")
	if strings.Contains(got, "..") || strings.Contains(got, "/") {
		t.Errorf("sanitize(%q) = %q, still contains path-escape characters", "../../etc/passwd", got)
	}
}

func TestWriteStubText(t *testing.T) {
	var buf bytes.Buffer
	WriteStub(&buf, "toon", &Hit{N: 3, Age: 2 * time.Minute})
	want := "# sf: already returned in call #3 (~2m ago); rerun with --force to repeat\n"
	if buf.String() != want {
		t.Errorf("WriteStub text = %q, want %q", buf.String(), want)
	}
}

func TestWriteStubJSON(t *testing.T) {
	var buf bytes.Buffer
	WriteStub(&buf, "json", &Hit{N: 3, Age: 2 * time.Minute})

	var v stubJSON
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &v); err != nil {
		t.Fatalf("stub json does not decode: %v\n%s", err, buf.String())
	}
	if !v.Dedup || v.DupOfCall != 3 || v.Age != "2m" {
		t.Errorf("decoded = %+v, want dedup=true dup_of_call=3 age=2m", v)
	}
	if !strings.Contains(v.Hint, "--force") || !strings.Contains(v.Hint, "force:true") {
		t.Errorf("hint must mention both --force and force:true, got %q", v.Hint)
	}
}

func TestAgeFormat(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{90 * time.Second, "2m"},
		{130 * time.Second, "2m"},
	}
	for _, tc := range cases {
		if got := formatAge(tc.d); got != tc.want {
			t.Errorf("formatAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
