package packagist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSemverRe(t *testing.T) {
	valid := []string{"2.1.0", "v2.1.0", "0.2.0", "12.6.0", "2.0.0-rc1", "1.0.0+build.5"}
	invalid := []string{"2.1", "2", "abc", "2.1.0.0", "", "dev-main", "v2"}
	for _, v := range valid {
		if !semverRe.MatchString(v) {
			t.Errorf("semverRe(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if semverRe.MatchString(v) {
			t.Errorf("semverRe(%q) = true, want false", v)
		}
	}
}

func TestCmpVer(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.0.0", "2.0.0", 0},
		{"2.0.0", "1.14.0", 1},
		{"1.14.0", "2.0.0", -1},
		{"1.9.0", "1.10.0", -1},   // numeric, not lexical
		{"v2.0.0", "2.0.0", 0},    // leading v
		{"2.0.0-rc1", "2.0.0", 0}, // pre-release suffix ignored
		{"12.6.0", "12.6", 0},     // missing parts = 0
	}
	for _, c := range cases {
		if got := cmpVer(c.a, c.b); got != c.want {
			t.Errorf("cmpVer(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestReleaseState(t *testing.T) {
	cases := []struct {
		local, packagist, want string
	}{
		{"", "", "no-tags"},
		{"2.0.0", "", "unpublished"},
		{"2.0.0", "2.0.0", "in-sync"},
		{"2.0.0", "1.0.0", "needs-update"}, // Packagist behind local tag
		{"1.0.0", "2.0.0", "local-stale"},  // Packagist ahead of local tag
	}
	for _, c := range cases {
		if got := releaseState(c.local, c.packagist); got != c.want {
			t.Errorf("releaseState(%q,%q) = %q, want %q", c.local, c.packagist, got, c.want)
		}
	}
}

func TestIsStable(t *testing.T) {
	stable := []string{"2.0.0", "1.14.0", "v3.0.1"}
	unstable := []string{"dev-main", "2.0.0-beta1", "1.0.0-RC1", "2.0.x-dev", "1.0.0-alpha"}
	for _, v := range stable {
		if !isStable(v) {
			t.Errorf("isStable(%q) = false, want true", v)
		}
	}
	for _, v := range unstable {
		if isStable(v) {
			t.Errorf("isStable(%q) = true, want false", v)
		}
	}
}

func TestLatestStable(t *testing.T) {
	body := []byte(`{"packages":{"acme/array-reader":[
		{"version":"dev-main"},
		{"version":"1.0.0"},
		{"version":"2.0.0"},
		{"version":"2.1.0-beta1"},
		{"version":"1.14.0"}
	]}}`)
	got, err := latestStable(body, "acme/array-reader")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0" {
		t.Errorf("latestStable = %q, want 2.0.0 (highest stable, dev/beta ignored)", got)
	}

	// Unknown package name → no versions → empty.
	if got, _ := latestStable(body, "acme/missing"); got != "" {
		t.Errorf("latestStable(missing) = %q, want empty", got)
	}
}

func TestSummarize(t *testing.T) {
	statuses := []Status{
		{State: "in-sync"},      // not drift
		{State: "skip-net"},     // not drift
		{State: "unknown"},      // not drift, counted separately
		{State: "unknown"},      // not drift, counted separately
		{State: "no-tags"},      // drift
		{State: "unpublished"},  // drift
		{State: "needs-update"}, // drift
		{State: "local-stale"},  // drift
	}
	drift, unknown := summarize(statuses)
	if drift != 4 {
		t.Errorf("drift = %d, want 4 (actionable only; in-sync/skip-net/unknown excluded)", drift)
	}
	if unknown != 2 {
		t.Errorf("unknown = %d, want 2", unknown)
	}
}

func TestRenderUnknownHeader(t *testing.T) {
	statuses := []Status{{Name: "acme/x", LocalTag: "1.0.0", Pushed: "?", State: "unknown"}}

	// unknown > 0: surfaced in both toon and md headers.
	for _, c := range []struct{ format, want string }{
		{"toon", "unknown=2"},
		{"md", "2 unknown"},
	} {
		var b strings.Builder
		if err := render(&b, c.format, statuses, 1, 2); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), c.want) {
			t.Errorf("%s header missing %q:\n%s", c.format, c.want, b.String())
		}
	}

	// unknown == 0: header unchanged (backward compatible — no unknown token).
	var b strings.Builder
	if err := render(&b, "toon", statuses, 1, 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "unknown=") {
		t.Errorf("toon header should omit unknown when 0:\n%s", b.String())
	}
}

func TestFetchWithRetry(t *testing.T) {
	ctx := context.Background()

	// Transient error twice, then success → one final success, three calls.
	t.Run("transient then success", func(t *testing.T) {
		calls := 0
		attempt := func(context.Context, string) (string, bool, error) {
			calls++
			if calls < 3 {
				return "", true, errors.New("boom")
			}
			return "1.0.0", false, nil
		}
		got, err := fetchWithRetry(ctx, "acme/x", attempt, 3, 0)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got != "1.0.0" {
			t.Errorf("got = %q, want 1.0.0", got)
		}
		if calls != 3 {
			t.Errorf("calls = %d, want 3", calls)
		}
	})

	// 404 → ("", false, nil): no retries, exactly one call.
	t.Run("404 no retries", func(t *testing.T) {
		calls := 0
		attempt := func(context.Context, string) (string, bool, error) {
			calls++
			return "", false, nil
		}
		got, err := fetchWithRetry(ctx, "acme/x", attempt, 3, 0)
		if err != nil || got != "" {
			t.Fatalf("got %q, err %v; want \"\", nil", got, err)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (no retries on 404)", calls)
		}
	})

	// Always retryable → error after exactly attempts calls.
	t.Run("exhaustion", func(t *testing.T) {
		calls := 0
		attempt := func(context.Context, string) (string, bool, error) {
			calls++
			return "", true, errors.New("boom")
		}
		_, err := fetchWithRetry(ctx, "acme/x", attempt, 3, 0)
		if err == nil {
			t.Fatal("err = nil, want error after exhausting attempts")
		}
		if calls != 3 {
			t.Errorf("calls = %d, want 3", calls)
		}
	})
}

func TestDoP2Request(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		retryAfter  string
		wantVersion string
		wantRetry   bool
		wantErr     bool
	}{
		{name: "404 unpublished", status: 404, wantVersion: "", wantRetry: false, wantErr: false},
		{name: "200 ok", status: 200,
			body:        `{"packages":{"acme/x":[{"version":"2.0.0"},{"version":"1.0.0"}]}}`,
			wantVersion: "2.0.0", wantRetry: false, wantErr: false},
		{name: "429 transient", status: 429, retryAfter: "2", wantRetry: true, wantErr: true},
		{name: "500 transient", status: 500, wantRetry: true, wantErr: true},
		{name: "403 fatal", status: 403, wantRetry: false, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if c.retryAfter != "" {
					w.Header().Set("Retry-After", c.retryAfter)
				}
				w.WriteHeader(c.status)
				if c.body != "" {
					_, _ = w.Write([]byte(c.body))
				}
			}))
			defer srv.Close()

			old := p2BaseURL
			p2BaseURL = srv.URL + "/"
			defer func() { p2BaseURL = old }()

			v, retryable, err := doP2Request(context.Background(), "acme/x")
			if v != c.wantVersion {
				t.Errorf("version = %q, want %q", v, c.wantVersion)
			}
			if retryable != c.wantRetry {
				t.Errorf("retryable = %v, want %v", retryable, c.wantRetry)
			}
			if (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr %v", err, c.wantErr)
			}
			if c.retryAfter != "" {
				var ra *retryAfterError
				if !errors.As(err, &ra) || ra.after != 2*time.Second {
					t.Errorf("Retry-After not honored: err = %v", err)
				}
			}
		})
	}
}

func TestCollect(t *testing.T) {
	root := tempPkgRepo(t, "acme/x", "v2.0.0")
	cases := []struct {
		name  string
		fetch fetcher
		want  string
	}{
		{"probe error", func(string) (string, error) { return "", errors.New("boom") }, "unknown"},
		{"not published", func(string) (string, error) { return "", nil }, "unpublished"},
		{"in sync", func(string) (string, error) { return "2.0.0", nil }, "in-sync"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			statuses, err := Collect(root, false, c.fetch)
			if err != nil {
				t.Fatal(err)
			}
			if len(statuses) != 1 {
				t.Fatalf("got %d packages, want 1", len(statuses))
			}
			if statuses[0].State != c.want {
				t.Errorf("State = %q, want %q", statuses[0].State, c.want)
			}
		})
	}
}

// tempPkgRepo builds a one-package tree under a temp dir: root/pkg with a
// composer.json, a git repo and tag. Returns the tree root for Collect.
func tempPkgRepo(t *testing.T, name, tag string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"name":"`+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "t")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "init")
	if tag != "" {
		runGit(t, dir, "tag", tag)
	}
	return root
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
