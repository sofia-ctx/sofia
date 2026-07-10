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

// githubAPIBase is the GitHub REST API host release-fetch talks to. Release
// metadata and asset downloads both go through it (an asset is fetched from
// its API endpoint with Accept: application/octet-stream, which redirects to a
// signed CDN URL) — the only path that works for a private repo, since the
// public github.com/.../releases/download/... URLs 404 without a browser
// session. It is a package var, not a constant, so tests can point it at an
// httptest server instead of the real GitHub.
var githubAPIBase = "https://api.github.com"

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
	rel, err := resolveRelease(ctx, owner, repo, ref)
	if err != nil {
		return "", "", err
	}

	assetURL, ok := rel.assets[asset]
	if !ok {
		return "", "", fmt.Errorf("release %s of %s/%s has no asset %q", rel.tag, owner, repo, asset)
	}
	tmpPath, err := downloadAsset(ctx, assetURL, installedDir)
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", asset, err)
	}
	defer func() { _ = os.Remove(tmpPath) }() // no-op once renamed into place below

	sumURL, ok := rel.assets["checksums.txt"]
	if !ok {
		return "", "", fmt.Errorf("release %s of %s/%s has no checksums.txt", rel.tag, owner, repo)
	}
	sums, err := downloadChecksums(ctx, sumURL)
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

// releaseGet performs one GET with the release User-Agent (and, when non-empty,
// the given Accept), through releaseHTTPClient so its redirect policy applies.
// When a GitHub token is in the environment it authenticates the request, which
// is what makes a private repo's release metadata and assets reachable; without
// one the request is unauthenticated and the public path is unchanged.
func releaseGet(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", releaseUserAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if tok := releaseToken(); tok != "" {
		// GitHub 302-redirects an authenticated asset download to a signed
		// CDN URL on a different host; net/http never forwards the
		// Authorization header across a host change, so the token reaches
		// only api.github.com and never the CDN.
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return releaseHTTPClient.Do(req)
}

// releaseToken returns a GitHub token for authenticating release-fetch against
// a private repo, read from the environment only: GH_TOKEN (the gh CLI's
// variable) takes precedence over GITHUB_TOKEN (the Actions default). Empty
// means unauthenticated — public repos need no token. sf never stores or
// writes the token; it is read per request and sent only to GitHub.
func releaseToken() string {
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// releaseMeta is a resolved GitHub release: its tag and a name→asset-URL map.
// The asset URLs are api.github.com/.../releases/assets/{id} endpoints;
// fetching one with Accept: application/octet-stream returns the file (via a
// signed-CDN redirect). This is deliberately not the public
// github.com/.../releases/download/... URL, which 404s for a private repo.
type releaseMeta struct {
	tag    string
	assets map[string]string
}

// resolveRelease fetches a repo's release metadata from GitHub's REST API: the
// release tagged ref, or — when ref is empty — the repo's latest release. The
// returned map lets fetchReleaseBinary look up an asset's download endpoint by
// filename, which is how it stays private-repo-safe.
func resolveRelease(ctx context.Context, owner, repo, ref string) (releaseMeta, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var path string
	if ref != "" {
		path = fmt.Sprintf("/repos/%s/%s/releases/tags/%s", owner, repo, ref)
	} else {
		path = fmt.Sprintf("/repos/%s/%s/releases/latest", owner, repo)
	}
	url := githubAPIBase + path
	resp, err := releaseGet(reqCtx, url, "application/vnd.github+json")
	if err != nil {
		return releaseMeta{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return releaseMeta{}, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return releaseMeta{}, err
	}
	var doc struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return releaseMeta{}, fmt.Errorf("parse release metadata: %w", err)
	}
	if doc.TagName == "" {
		return releaseMeta{}, fmt.Errorf("release metadata for %s/%s has no tag_name", owner, repo)
	}
	assets := make(map[string]string, len(doc.Assets))
	for _, a := range doc.Assets {
		assets[a.Name] = a.URL
	}
	return releaseMeta{tag: doc.TagName, assets: assets}, nil
}

// downloadChecksums fetches and parses a release's checksums.txt from its API
// asset endpoint; Accept: application/octet-stream makes GitHub serve the file
// bytes (via a redirect) rather than the asset's JSON metadata.
func downloadChecksums(ctx context.Context, url string) (map[string]string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := releaseGet(reqCtx, url, "application/octet-stream")
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
// returning its path. url is an API asset endpoint; Accept:
// application/octet-stream makes GitHub serve the binary (via a redirect)
// rather than the asset's JSON metadata. The caller is responsible for
// removing the temp file on any path that doesn't end in a successful rename.
func downloadAsset(ctx context.Context, url, dir string) (path string, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := releaseGet(reqCtx, url, "application/octet-stream")
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
