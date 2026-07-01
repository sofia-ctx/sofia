package packagist

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/composer"
	"github.com/sofia-ctx/sofia/internal/envfile"
	"github.com/sofia-ctx/sofia/internal/toon"
)

// ReleaseOptions controls a `packagist release` run.
type ReleaseOptions struct {
	Root       string
	Target     string // package name / dir basename
	Version    string // semver tag to create/push (e.g. 2.1.0)
	Message    string // annotated-tag message (default: "Release <version>")
	Username   string // Packagist API username (default: the vendor prefix of the resolved package's composer.json name)
	AllowDirty bool
	DryRun     bool
	Timeout    time.Duration // verify poll budget
}

// ReleaseResult is the step-by-step outcome of a release.
type ReleaseResult struct {
	Package          string   `json:"package"`
	Dir              string   `json:"dir"`
	Version          string   `json:"version"`
	TagCreated       bool     `json:"tag_created"`
	TagPushed        bool     `json:"tag_pushed"`
	PackagistUpdated bool     `json:"packagist_updated"`
	Verified         bool     `json:"verified"`
	Packagist        string   `json:"packagist"`
	Steps            []string `json:"steps"`
}

var semverRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?$`)

// RunRelease tags + pushes + triggers a Packagist update for one package, then
// verifies the new version appears. Mutating + network; honour --dry-run first.
func RunRelease(opts ReleaseOptions, w io.Writer) error {
	if opts.Root == "" {
		opts.Root = "."
	}
	if opts.Message == "" {
		opts.Message = "Release " + opts.Version
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 90 * time.Second
	}
	tracker := calllog.Start("packagist release", []string{opts.Target, opts.Version, fmt.Sprintf("--dry-run=%v", opts.DryRun)})

	res, err := release(opts)
	if res != nil {
		cw := &calllog.Counter{W: w}
		renderRelease(cw, res, opts.DryRun)
		tracker.SetSummary(map[string]any{"package": res.Package, "version": res.Version, "verified": res.Verified, "dry_run": opts.DryRun})
		tracker.RecordOutput(cw)
	}
	tracker.Finish(err)
	return err
}

func release(opts ReleaseOptions) (*ReleaseResult, error) {
	if !semverRe.MatchString(opts.Version) {
		return nil, fmt.Errorf("packagist release: version %q is not semver (e.g. 2.1.0)", opts.Version)
	}

	pkgs, err := composer.Collect(opts.Root)
	if err != nil {
		return nil, err
	}
	var pkg composer.Pkg
	for _, p := range pkgs {
		if p.Name == opts.Target || strings.HasSuffix(p.Name, "/"+opts.Target) || filepath.Base(p.Dir) == opts.Target {
			pkg = p
			break
		}
	}
	if pkg.Name == "" {
		return nil, fmt.Errorf("packagist release: no package matching %q under %s", opts.Target, opts.Root)
	}
	if opts.Username == "" {
		if vendor, _, ok := strings.Cut(pkg.Name, "/"); ok && vendor != "" {
			opts.Username = vendor
		} else {
			return nil, fmt.Errorf("packagist release: can't derive a Packagist username from package name %q; pass --username", pkg.Name)
		}
	}

	dir := filepath.Join(opts.Root, pkg.Dir)
	res := &ReleaseResult{Package: pkg.Name, Dir: pkg.Dir, Version: opts.Version}
	step := func(format string, a ...any) { res.Steps = append(res.Steps, fmt.Sprintf(format, a...)) }

	// Resolve the API token early (unless dry-run) so we fail before touching git.
	token := ""
	if !opts.DryRun {
		if token, err = resolveToken(); err != nil {
			return res, err
		}
	}

	// Preflight: a clean working tree, so the tag points at a known state.
	if !opts.AllowDirty {
		if out, _ := git(dir, 5*time.Second, "status", "--porcelain"); strings.TrimSpace(out) != "" {
			return res, fmt.Errorf("packagist release: %s working tree is not clean (commit/stash, or pass --allow-dirty)", pkg.Dir)
		}
	}

	// 1) Tag (annotated), reusing an existing tag of the same name.
	tagExists := false
	if out, _ := git(dir, 5*time.Second, "tag", "--list", opts.Version); strings.TrimSpace(out) != "" {
		tagExists = true
	}
	if tagExists {
		step("tag %s already exists — reusing", opts.Version)
	} else if opts.DryRun {
		step("would create annotated tag %s (%q)", opts.Version, opts.Message)
	} else {
		if _, err := git(dir, 10*time.Second, "tag", "-a", opts.Version, "-m", opts.Message); err != nil {
			return res, err
		}
		res.TagCreated = true
		step("created annotated tag %s", opts.Version)
	}

	// 2) Push the tag to origin.
	if opts.DryRun {
		step("would run: git push origin %s", opts.Version)
	} else {
		if _, err := git(dir, 60*time.Second, "push", "origin", opts.Version); err != nil {
			return res, err
		}
		res.TagPushed = true
		step("pushed tag %s to origin", opts.Version)
	}

	// 3) Trigger the Packagist update-package webhook (it does not auto-fire
	//    for every package).
	repoURL := "https://github.com/" + pkg.Name
	if opts.DryRun {
		step("would POST update-package for %s (username=%s, token=***)", repoURL, opts.Username)
	} else {
		if err := updatePackage(opts.Username, token, repoURL); err != nil {
			return res, err
		}
		res.PackagistUpdated = true
		step("triggered Packagist update-package for %s", pkg.Name)
	}

	// 4) Verify the new version shows up on Packagist (best-effort poll).
	if opts.DryRun {
		step("would poll p2/%s.json until %s appears (≤%s)", pkg.Name, opts.Version, opts.Timeout)
		return res, nil
	}
	res.Packagist, res.Verified = verifyPublished(pkg.Name, opts.Version, opts.Timeout)
	if res.Verified {
		step("verified %s on Packagist (latest=%s)", opts.Version, res.Packagist)
	} else {
		step("not yet visible on Packagist (latest=%s); crawl may lag — re-check with `sf packagist status`", orDash(res.Packagist))
	}
	return res, nil
}

// resolveToken reads PACKAGIST_API_TOKEN from the environment, else from
// ~/.config/sofia/packagist.env (the documented location).
func resolveToken() (string, error) {
	if v := os.Getenv("PACKAGIST_API_TOKEN"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".config", "sofia", "packagist.env")
	vals, err := envfile.Load(path)
	if err != nil {
		return "", fmt.Errorf("packagist release: no PACKAGIST_API_TOKEN in env and cannot read %s: %w", path, err)
	}
	if t := vals["PACKAGIST_API_TOKEN"]; t != "" {
		return t, nil
	}
	return "", fmt.Errorf("packagist release: PACKAGIST_API_TOKEN not set and not found in %s", path)
}

// updatePackage POSTs the Packagist update-package webhook.
func updatePackage(username, token, repoURL string) error {
	api := fmt.Sprintf("https://packagist.org/api/update-package?username=%s&apiToken=%s", username, token)
	body := fmt.Sprintf(`{"repository":{"url":%q}}`, repoURL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("packagist update-package: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(out)))
	}
	if !bytes.Contains(out, []byte("success")) {
		return fmt.Errorf("packagist update-package: unexpected response: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// verifyPublished polls p2 until version is published (cmp >= 0) or timeout.
func verifyPublished(name, version string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	latest := ""
	for {
		if v, err := fetchPackagistLatest(name); err == nil {
			latest = v
			if v != "" && cmpVer(v, version) >= 0 {
				return v, true
			}
		}
		if time.Now().After(deadline) {
			return latest, false
		}
		time.Sleep(5 * time.Second)
	}
}

func renderRelease(w io.Writer, r *ReleaseResult, dryRun bool) {
	mode := "release"
	if dryRun {
		mode = "release (dry-run)"
	}
	fmt.Fprintf(w, "# %s %s@%s\n", mode, orDash(r.Package), r.Version)
	fmt.Fprintf(w, "result{tag_created,tag_pushed,packagist_updated,verified,packagist}:\n")
	fmt.Fprintf(w, "%s%v,%v,%v,%v,%s\n", toon.Indent,
		r.TagCreated, r.TagPushed, r.PackagistUpdated, r.Verified, toon.Scalar(orDash(r.Packagist)))
	fmt.Fprintf(w, "steps[%d]:\n", len(r.Steps))
	for _, s := range r.Steps {
		fmt.Fprintf(w, "%s%s\n", toon.Indent, s)
	}
}
