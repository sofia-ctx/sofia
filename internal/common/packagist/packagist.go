// Package packagist implements `sf packagist status` — per-package release
// health for a tree of PHP packages: the latest local git tag vs whether that
// tag is pushed to origin vs the latest version published on Packagist. It
// answers the recurring "which packages still need a tag or a Packagist
// update?" question (the webhook does not auto-fire for every package) without
// re-deriving the publish recipe each time.
package packagist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/common/composer"
	"github.com/sofia-ctx/sofia/internal/toon"
)

// Options controls a `packagist status` run.
type Options struct {
	Root    string
	Format  string
	Offline bool // skip Packagist/remote network probes (tags only)
}

// Status is one package's release health.
type Status struct {
	Name      string `json:"name"`
	Dir       string `json:"dir"`
	LocalTag  string `json:"local_tag"`
	Pushed    string `json:"pushed"`    // yes | no | ?
	Packagist string `json:"packagist"` // latest published version, "" = none/unknown
	State     string `json:"state"`
}

// fetcher resolves the latest published version for a package name; swapped in
// tests. Returns "" when the package is not published (404).
type fetcher func(name string) (string, error)

// Run collects release status for every package under root and renders it.
func Run(opts Options, w io.Writer) error {
	root := opts.Root
	if root == "" {
		root = "."
	}
	tracker := calllog.Start("packagist status", []string{"--format=" + opts.Format, root})

	statuses, err := Collect(root, opts.Offline, fetchPackagistLatest)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	drift, unknown := summarize(statuses)
	tracker.SetSummary(map[string]any{"packages": len(statuses), "drift": drift, "unknown": unknown})

	cw := &calllog.Counter{W: w}
	renderErr := render(cw, opts.Format, statuses, drift, unknown)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// Collect builds the release status for each package under root. The fetcher is
// injectable for testing; when offline, Packagist/remote probes are skipped.
func Collect(root string, offline bool, fetch fetcher) ([]Status, error) {
	pkgs, err := composer.Collect(root)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(pkgs))
	for _, p := range pkgs {
		dir := filepath.Join(root, p.Dir)
		s := Status{Name: p.Name, Dir: p.Dir, LocalTag: latestTag(dir), Pushed: "?"}

		if offline {
			s.State = "skip-net"
			out = append(out, s)
			continue
		}
		s.Pushed = pushedState(dir, s.LocalTag)
		if p.Name != "" {
			v, err := fetch(p.Name)
			if err != nil {
				// Probe failed (network/timeout/5xx after retries): we could
				// not verify — this is NOT the same as a 404/unpublished.
				s.State = "unknown"
				out = append(out, s)
				continue
			}
			s.Packagist = v
		}
		s.State = releaseState(s.LocalTag, s.Packagist)
		out = append(out, s)
	}
	return out, nil
}

// releaseState classifies a package from its local tag and Packagist version.
func releaseState(localTag, packagist string) string {
	if localTag == "" {
		return "no-tags"
	}
	if packagist == "" {
		return "unpublished"
	}
	switch cmpVer(packagist, localTag) {
	case 0:
		return "in-sync"
	case -1:
		return "needs-update" // Packagist behind the local tag
	default:
		return "local-stale" // Packagist newer than the local tag (pull)
	}
}

// summarize counts actionable drift and unverifiable packages. Only actionable
// states count as drift; in-sync, skip-net and unknown do not (so a transient
// probe failure or --offline run no longer inflates the drift gate).
func summarize(statuses []Status) (drift, unknown int) {
	for _, s := range statuses {
		switch s.State {
		case "no-tags", "unpublished", "needs-update", "local-stale":
			drift++
		case "unknown":
			unknown++
		}
	}
	return drift, unknown
}

func latestTag(dir string) string {
	out, err := git(dir, 3*time.Second, "describe", "--tags", "--abbrev=0")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// pushedState reports whether localTag exists on origin: yes | no | ? (when the
// remote probe fails, e.g. no network/auth in the sandbox).
func pushedState(dir, localTag string) string {
	if localTag == "" {
		return "?"
	}
	out, err := git(dir, 5*time.Second, "ls-remote", "--tags", "origin", "refs/tags/"+localTag)
	if err != nil {
		return "?"
	}
	if strings.TrimSpace(out) == "" {
		return "no"
	}
	return "yes"
}

// git runs a git subcommand in dir with a timeout and no interactive prompts.
func git(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return out.String(), nil
}

// p2BaseURL is the Packagist p2 endpoint prefix; overridden in tests.
var p2BaseURL = "https://repo.packagist.org/p2/"

// retryAfterError carries a server-suggested delay (HTTP 429 Retry-After) so
// the retry loop can honor it while doP2Request keeps a plain 3-value return.
type retryAfterError struct {
	after time.Duration
	err   error
}

func (e *retryAfterError) Error() string { return e.err.Error() }
func (e *retryAfterError) Unwrap() error { return e.err }

// p2Attempt is one Packagist p2 fetch attempt; swapped in tests to drive the
// retry loop without a network. retryable reports whether err is transient and
// worth retrying.
type p2Attempt func(ctx context.Context, name string) (version string, retryable bool, err error)

// fetchPackagistLatest returns the highest stable version published on
// Packagist for name, or "" when not published (404). Transient failures
// (network/timeout/429/5xx) are retried with backoff so a flaky network does
// not masquerade as "unpublished".
func fetchPackagistLatest(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return fetchWithRetry(ctx, name, doP2Request, 3, 200*time.Millisecond)
}

// fetchWithRetry runs attempt up to attempts times, retrying only transient
// failures with exponential backoff + jitter (a 429 Retry-After overrides the
// computed pause). A non-retryable result or success returns immediately.
func fetchWithRetry(ctx context.Context, name string, attempt p2Attempt, attempts int, base time.Duration) (string, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		v, retryable, err := attempt(ctx, name)
		if err == nil {
			return v, nil
		}
		lastErr = err
		if !retryable || i == attempts-1 {
			break
		}
		wait := backoff(base, i)
		var ra *retryAfterError
		if errors.As(err, &ra) && ra.after > 0 {
			wait = ra.after
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "", lastErr
}

// backoff returns an exponential delay (base, base*3, base*9, …) with equal
// jitter to avoid synchronized retries. base <= 0 yields 0 (fast tests).
func backoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		return 0
	}
	d := base
	for i := 0; i < attempt; i++ {
		d *= 3
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// doP2Request performs one Packagist p2 fetch. Mapping: 404 → ("",false,nil)
// (genuinely unpublished); 200 → parsed version (parse error is not retryable);
// 429/5xx/network/timeout → ("",true,err) (transient); other 4xx (403/410) →
// ("",false,err).
func doP2Request(ctx context.Context, name string) (string, bool, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	url := p2BaseURL + name + ".json"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", true, err // network/timeout — transient
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", false, nil
	case resp.StatusCode == http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
		if err != nil {
			return "", true, err // read interrupted — transient
		}
		v, err := latestStable(body, name)
		return v, false, err
	case resp.StatusCode == http.StatusTooManyRequests:
		err := fmt.Errorf("packagist %s: HTTP %d", name, resp.StatusCode)
		return "", true, &retryAfterError{after: retryAfter(resp), err: err}
	case resp.StatusCode >= 500:
		return "", true, fmt.Errorf("packagist %s: HTTP %d", name, resp.StatusCode)
	default:
		return "", false, fmt.Errorf("packagist %s: HTTP %d", name, resp.StatusCode)
	}
}

// retryAfter reads a Retry-After header as whole seconds (0 if absent/invalid).
func retryAfter(resp *http.Response) time.Duration {
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 0
}

type p2Doc struct {
	Packages map[string][]struct {
		Version string `json:"version"`
	} `json:"packages"`
}

// latestStable parses a Packagist p2 document and returns the highest stable
// version (dev/branch versions ignored).
func latestStable(body []byte, name string) (string, error) {
	var doc p2Doc
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", err
	}
	best := ""
	for _, v := range doc.Packages[name] {
		if !isStable(v.Version) {
			continue
		}
		if best == "" || cmpVer(v.Version, best) > 0 {
			best = v.Version
		}
	}
	return best, nil
}

func isStable(v string) bool {
	low := strings.ToLower(v)
	if strings.HasPrefix(low, "dev-") || strings.Contains(low, "-dev") {
		return false
	}
	for _, m := range []string{"alpha", "beta", "rc"} {
		if strings.Contains(low, m) {
			return false
		}
	}
	return true
}

// cmpVer compares two dotted version strings numerically (leading v stripped,
// pre-release suffix ignored): -1, 0, or 1.
func cmpVer(a, b string) int {
	pa, pb := verParts(a), verParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func verParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

var fields = []string{"pkg", "local_tag", "pushed", "packagist", "state"}

func render(w io.Writer, format string, statuses []Status, drift, unknown int) error {
	switch format {
	case "", "toon":
		fmt.Fprintf(w, "release[%d]{%s}: # drift=%d", len(statuses), strings.Join(fields, ","), drift)
		if unknown > 0 {
			fmt.Fprintf(w, " unknown=%d", unknown)
		}
		fmt.Fprintln(w)
		for _, s := range statuses {
			fmt.Fprintf(w, "%s%s,%s,%s,%s,%s\n",
				toon.Indent,
				toon.Scalar(orDash(s.Name)),
				toon.Scalar(orDash(s.LocalTag)),
				toon.Scalar(s.Pushed),
				toon.Scalar(orDash(s.Packagist)),
				toon.Scalar(s.State),
			)
		}
		return nil
	case "md":
		if unknown > 0 {
			fmt.Fprintf(w, "# packagist status (%d packages, %d out of sync, %d unknown)\n\n", len(statuses), drift, unknown)
		} else {
			fmt.Fprintf(w, "# packagist status (%d packages, %d out of sync)\n\n", len(statuses), drift)
		}
		fmt.Fprintln(w, "| Package | Local tag | Pushed | Packagist | State |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
		for _, s := range statuses {
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n",
				orDash(s.Name), orDash(s.LocalTag), s.Pushed, orDash(s.Packagist), s.State)
		}
		return nil
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(statuses)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
