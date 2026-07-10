package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/gitclone"
)

// githubAPIBase and githubDLBase are the GitHub API and release-download
// hosts release-fetch talks to. They are package vars, not constants, so
// tests can point them at an httptest server instead of the real GitHub.
var (
	githubAPIBase = "https://api.github.com"
	githubDLBase  = "https://github.com"
)

// releaseUserAgent identifies sf to GitHub's API — required for
// unauthenticated requests, and generally good manners.
const releaseUserAgent = "sofia-sf-plugin"

// releaseHTTPClient rejects any redirect whose target isn't https. The
// initial request goes to the configured (trusted) base var above — https in
// production, a plain-http httptest server in tests — but a real release
// asset legitimately redirects to a CDN host (objects.githubusercontent.com),
// and that hop must stay encrypted regardless of how the first request was
// made.
var releaseHTTPClient = &http.Client{
	CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("refusing a non-https redirect to %s", req.URL)
		}
		return nil
	},
}

// assetName expands a release.asset template's {os}/{arch} placeholders with
// this build's runtime.GOOS/GOARCH — matching goreleaser's {{.Os}}/{{.Arch}}
// naming convention.
func assetName(tmpl string) string {
	r := strings.NewReplacer("{os}", runtime.GOOS, "{arch}", runtime.GOARCH)
	return r.Replace(tmpl)
}

// fetchReleaseBinary downloads the prebuilt executable a manifest's release
// block declares, verifies it against the release's checksums.txt, and
// installs it into installedDir as the plugin's executable. url is the
// plugin's install URL (used to derive owner/repo unless m.Release.GitHub
// overrides it); ref is the install's pinned branch/tag, reused as the
// release tag when set, else the repo's latest release is resolved. It
// returns the resolved asset filename and the binary's lowercase-hex sha256.
func fetchReleaseBinary(url, ref, installedDir string, m Manifest) (asset, sha string, err error) {
	owner, repo, err := releaseRepo(url, m)
	if err != nil {
		return "", "", err
	}
	asset = assetName(m.Release.Asset)
	if asset == "" {
		return "", "", fmt.Errorf("release.asset is required")
	}

	ctx := context.Background()
	tag := ref
	if tag == "" {
		tag, err = latestReleaseTag(ctx, owner, repo)
		if err != nil {
			return "", "", fmt.Errorf("resolve latest release for %s/%s: %w", owner, repo, err)
		}
	}

	base := fmt.Sprintf("%s/%s/%s/releases/download/%s", githubDLBase, owner, repo, tag)
	tmpPath, err := downloadAsset(ctx, base+"/"+asset, installedDir)
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", asset, err)
	}
	defer func() { _ = os.Remove(tmpPath) }() // no-op once renamed into place below

	sums, err := downloadChecksums(ctx, base+"/checksums.txt")
	if err != nil {
		return "", "", fmt.Errorf("download checksums.txt: %w", err)
	}
	want, ok := sums[asset]
	if !ok {
		return "", "", fmt.Errorf("checksums.txt has no entry for %s", asset)
	}
	got, err := sha256File(tmpPath)
	if err != nil {
		return "", "", err
	}
	if !strings.EqualFold(got, want) {
		return "", "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}

	execName := m.Exec
	if execName == "" {
		execName = filepath.Base(installedDir)
	}
	dst := filepath.Join(installedDir, filepath.FromSlash(execName))
	if err := os.Rename(tmpPath, dst); err != nil {
		return "", "", err
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		return "", "", err
	}
	return asset, got, nil // sha256File already returns lowercase hex
}

// releaseRepo resolves the GitHub owner/repo a release lives under: an
// explicit m.Release.GitHub override, or else parsed from the plugin's
// install url. Only github.com is supported for the inferred case (an
// override sidesteps the host — it names owner/repo directly, not a URL).
func releaseRepo(url string, m Manifest) (owner, repo string, err error) {
	if gh := strings.TrimSpace(m.Release.GitHub); gh != "" {
		parts := strings.SplitN(gh, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("release.github must be \"owner/repo\", got %q", gh)
		}
		return parts[0], parts[1], nil
	}
	host, owner, repo, err := gitclone.RepoSlug(url)
	if err != nil {
		return "", "", err
	}
	if host != "github.com" {
		return "", "", fmt.Errorf("release-fetch supports only github.com repos (install URL host is %q; set release.github to override)", host)
	}
	return owner, repo, nil
}

// releaseGet performs one GET with the release User-Agent, through
// releaseHTTPClient (so its redirect policy applies).
func releaseGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", releaseUserAgent)
	return releaseHTTPClient.Do(req)
}

// latestReleaseTag resolves a repo's latest-release tag via GitHub's REST
// API, for a plugin install with no pinned ref.
func latestReleaseTag(ctx context.Context, owner, repo string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPIBase, owner, repo)
	resp, err := releaseGet(reqCtx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var doc struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("parse release metadata: %w", err)
	}
	if doc.TagName == "" {
		return "", fmt.Errorf("release metadata for %s/%s has no tag_name", owner, repo)
	}
	return doc.TagName, nil
}

// downloadChecksums fetches and parses a release's checksums.txt.
func downloadChecksums(ctx context.Context, url string) (map[string]string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := releaseGet(reqCtx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseChecksums(string(body)), nil
}

// parseChecksums parses a sha256sum-style checksums.txt: "<hex>  <filename>"
// per line (two spaces conventionally; any whitespace run is tolerated). A
// leading "*" on the filename (sha256sum's binary-mode marker) is stripped.
// Malformed lines are skipped rather than erroring — forward-compatible with
// whatever extra columns a future goreleaser template adds.
func parseChecksums(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

// downloadAsset streams url's body into a temp file created inside dir (so
// the later rename into the plugin dir is same-filesystem and atomic),
// returning its path. The caller is responsible for removing it on any path
// that doesn't end in a successful rename.
func downloadAsset(ctx context.Context, url, dir string) (path string, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := releaseGet(reqCtx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}

	f, err := os.CreateTemp(dir, ".sf-release-download-*")
	if err != nil {
		return "", err
	}
	path = f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

// sha256File hashes a file's contents. A local copy of pack's unexported
// sha256File (internal/pack/receipt.go) — small enough, and pulling in the
// pack package here would be the wrong dependency direction.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
