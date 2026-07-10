package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAssetName(t *testing.T) {
	cases := []struct{ tmpl, want string }{
		{"tool_{os}_{arch}", "tool_" + runtime.GOOS + "_" + runtime.GOARCH},
		{"tool", "tool"},
		{"{os}/{arch}/tool", runtime.GOOS + "/" + runtime.GOARCH + "/tool"},
		{"", ""},
	}
	for _, c := range cases {
		if got := assetName(c.tmpl); got != c.want {
			t.Errorf("assetName(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

// releaseServer spins an httptest server serving a fake GitHub API + release
// downloads for owner/repo "o"/"r" at tag "v1.2.3": a latest-release lookup,
// an asset, and its checksums.txt. latestHits counts calls to
// /repos/o/r/releases/latest, so a test can assert that path was (or wasn't)
// consulted (e.g. when a ref pins the tag).
func releaseServer(t *testing.T, assetName, assetBody, checksumsBody string) (srv *httptest.Server, latestHits *int64) {
	t.Helper()
	var hits int64
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.2.3"})
	})
	mux.HandleFunc("/o/r/releases/download/v1.2.3/"+assetName, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(assetBody))
	})
	mux.HandleFunc("/o/r/releases/download/v1.2.3/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(checksumsBody))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestFetchReleaseBinary(t *testing.T) {
	tmpl := "tool_{os}_{arch}"
	asset := assetName(tmpl)
	body := "#!/bin/sh\necho fake binary\n"
	sum := sha256Hex(body)

	setBases := func(t *testing.T, url string) {
		t.Helper()
		oldAPI, oldDL := githubAPIBase, githubDLBase
		githubAPIBase, githubDLBase = url, url
		t.Cleanup(func() { githubAPIBase, githubDLBase = oldAPI, oldDL })
	}

	t.Run("happy path: latest release, correct checksum", func(t *testing.T) {
		srv, latestHits := releaseServer(t, asset, body, fmt.Sprintf("%s  %s\n", sum, asset))
		setBases(t, srv.URL)

		installedDir := t.TempDir()
		m := Manifest{Exec: "foo", Release: &Release{Asset: tmpl}}
		gotAsset, gotSHA, err := fetchReleaseBinary("https://github.com/o/r", "", installedDir, m)
		if err != nil {
			t.Fatalf("fetchReleaseBinary: %v", err)
		}
		if gotAsset != asset {
			t.Errorf("asset = %q, want %q", gotAsset, asset)
		}
		if gotSHA != sum {
			t.Errorf("sha = %q, want %q", gotSHA, sum)
		}
		if atomic.LoadInt64(latestHits) != 1 {
			t.Errorf("expected the latest-release endpoint to be hit once, got %d", *latestHits)
		}

		dst := filepath.Join(installedDir, "foo")
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("binary not installed: %v", err)
		}
		if string(data) != body {
			t.Errorf("installed binary content = %q, want %q", data, body)
		}
		if !isExecutable(dst) {
			t.Errorf("installed binary is not executable: %s", dst)
		}
	})

	t.Run("ref given skips the latest-release lookup", func(t *testing.T) {
		srv, latestHits := releaseServer(t, asset, body, fmt.Sprintf("%s  %s\n", sum, asset))
		setBases(t, srv.URL)

		installedDir := t.TempDir()
		m := Manifest{Exec: "foo", Release: &Release{Asset: tmpl}}
		if _, _, err := fetchReleaseBinary("https://github.com/o/r", "v1.2.3", installedDir, m); err != nil {
			t.Fatalf("fetchReleaseBinary: %v", err)
		}
		if atomic.LoadInt64(latestHits) != 0 {
			t.Errorf("a pinned ref must not consult the latest-release endpoint, got %d hits", *latestHits)
		}
	})

	t.Run("checksum mismatch: error, nothing installed", func(t *testing.T) {
		srv, _ := releaseServer(t, asset, body, fmt.Sprintf("%s  %s\n", sha256Hex("wrong"), asset))
		setBases(t, srv.URL)

		installedDir := t.TempDir()
		m := Manifest{Exec: "foo", Release: &Release{Asset: tmpl}}
		if _, _, err := fetchReleaseBinary("https://github.com/o/r", "v1.2.3", installedDir, m); err == nil {
			t.Fatal("expected a checksum-mismatch error")
		}
		assertDirEmpty(t, installedDir)
	})

	t.Run("asset missing from checksums.txt: error, nothing installed", func(t *testing.T) {
		srv, _ := releaseServer(t, asset, body, sha256Hex(body)+"  some-other-file\n")
		setBases(t, srv.URL)

		installedDir := t.TempDir()
		m := Manifest{Exec: "foo", Release: &Release{Asset: tmpl}}
		_, _, err := fetchReleaseBinary("https://github.com/o/r", "v1.2.3", installedDir, m)
		if err == nil {
			t.Fatal("expected an error when the asset has no checksums.txt entry")
		}
		if !strings.Contains(err.Error(), "checksums.txt") {
			t.Errorf("error should mention checksums.txt: %v", err)
		}
		assertDirEmpty(t, installedDir)
	})

	t.Run("empty release.asset is rejected before any network call", func(t *testing.T) {
		installedDir := t.TempDir()
		m := Manifest{Exec: "foo", Release: &Release{}}
		if _, _, err := fetchReleaseBinary("https://github.com/o/r", "v1.2.3", installedDir, m); err == nil {
			t.Fatal("expected an error for an empty release.asset")
		}
	})

	t.Run("release.github overrides the URL-derived owner/repo", func(t *testing.T) {
		srv, _ := releaseServer(t, asset, body, fmt.Sprintf("%s  %s\n", sum, asset))
		setBases(t, srv.URL)

		installedDir := t.TempDir()
		m := Manifest{Exec: "foo", Release: &Release{Asset: tmpl, GitHub: "o/r"}}
		// A URL whose host/path bear no relation to o/r — only the override matters.
		if _, _, err := fetchReleaseBinary("https://example.com/nothing", "v1.2.3", installedDir, m); err != nil {
			t.Fatalf("fetchReleaseBinary with release.github override: %v", err)
		}
	})

	t.Run("non-github.com host is rejected without an override", func(t *testing.T) {
		installedDir := t.TempDir()
		m := Manifest{Exec: "foo", Release: &Release{Asset: tmpl}}
		_, _, err := fetchReleaseBinary("https://gitlab.com/o/r", "v1.2.3", installedDir, m)
		if err == nil || !strings.Contains(err.Error(), "github.com") {
			t.Errorf("expected a github.com-only error, got %v", err)
		}
	})
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected no leftover files in %s, found %v", dir, names)
	}
}

func TestParseChecksums(t *testing.T) {
	text := "abc123  tool_linux_amd64\n" +
		"def456 *tool_darwin_arm64\n" +
		"\n" +
		"not-a-valid-line\n"
	got := parseChecksums(text)
	want := map[string]string{
		"tool_linux_amd64":  "abc123",
		"tool_darwin_arm64": "def456",
	}
	if len(got) != len(want) {
		t.Fatalf("parseChecksums = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseChecksums[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// TestInstallFromGit_ReleaseFetch wires the whole gate end-to-end: a real git
// repo whose plugin.yaml declares exec+release and ships no binary is cloned
// via InstallFromGit, and the fake GitHub server above supplies the asset and
// checksums.txt. The installed plugin must end up enabled with the fetched
// executable, and .sf-origin.json must record the asset and its sha256.
func TestInstallFromGit_ReleaseFetch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)

	tmpl := "tool_{os}_{arch}"
	asset := assetName(tmpl)
	body := "#!/bin/sh\necho fake binary\n"
	sum := sha256Hex(body)
	srv, _ := releaseServer(t, asset, body, fmt.Sprintf("%s  %s\n", sum, asset))

	oldAPI, oldDL := githubAPIBase, githubDLBase
	githubAPIBase, githubDLBase = srv.URL, srv.URL
	t.Cleanup(func() { githubAPIBase, githubDLBase = oldAPI, oldDL })

	repo := releaseFetchRepo(t, tmpl)
	// The clone itself is a real local repo (git needs a real URL to clone);
	// release.github in its manifest points the release-fetch step at the
	// fake "o/r" the server above serves, independent of the clone URL.
	name, err := InstallFromGit("file://"+repo, "")
	if err != nil {
		t.Fatalf("InstallFromGit: %v", err)
	}
	if name != filepath.Base(repo) {
		t.Errorf("installed name = %q, want %q", name, filepath.Base(repo))
	}

	dst := filepath.Join(PluginsDir(), name, "tool")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("release binary not installed: %v", err)
	}
	if string(got) != body {
		t.Errorf("installed binary content = %q, want %q", got, body)
	}
	if !isExecutable(dst) {
		t.Errorf("installed binary is not executable: %s", dst)
	}

	if d, ok := Find(Load(), name); !ok || !d.Enabled {
		t.Errorf("release-fetched plugin not enabled: %+v", d)
	}

	o, err := readOrigin(name)
	if err != nil {
		t.Fatalf("readOrigin: %v", err)
	}
	if o.Asset != asset || o.SHA256 != sum {
		t.Errorf("origin = %+v, want asset=%q sha256=%q", o, asset, sum)
	}
}

// releaseFetchRepo commits a plugin.yaml declaring exec+release (and no
// binary) into a fresh git repo named "o-r-plugin", for InstallFromGit's
// clone step (a real clone; only the release download is faked).
// release.github pins the release-fetch step to the fake "o/r" repo the test
// server serves, independent of the (real, local) clone URL.
func releaseFetchRepo(t *testing.T, assetTmpl string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "o-r-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema: 1\nprotocol: \"1.1.0\"\ndescription: release-fetched tool\nexec: tool\nrelease:\n  asset: \"" + assetTmpl + "\"\n  github: \"o/r\"\n"
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "init", "--quiet")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "--quiet", "-m", "init")
	return dir
}
