package composer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/pkg/toon"
)

// CheckOptions controls a `composer check` run.
type CheckOptions struct {
	Root   string // tree to scan (default: cwd)
	Target string // optional single package (name suffix or dir basename); empty = all
	Format string
}

// CheckResult is the outcome of running one package's `check` script.
type CheckResult struct {
	Name       string `json:"name"`
	Dir        string `json:"dir"`
	OK         bool   `json:"ok"`
	Exit       int    `json:"exit"`
	DurationMs int64  `json:"dur_ms"`
	Fail       string `json:"fail,omitempty"`
}

var failLineRe = regexp.MustCompile(`(?i)\b(fail(ed|ures)?|error|errors|exception|fatal|not ok)\b|^\s*[✗✘]`)

// RunCheck runs each selected package's own `composer check` gate and renders a
// compact pass/fail summary instead of the full phpunit/phpstan/cs output.
func RunCheck(opts CheckOptions, w io.Writer) error {
	root := opts.Root
	if root == "" {
		root = "."
	}
	tracker := calllog.Start("composer check", []string{opts.Target, "--format=" + opts.Format})

	pkgs, err := Collect(root)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	if opts.Target != "" {
		pkgs = filterTarget(pkgs, opts.Target)
		if len(pkgs) == 0 {
			err := fmt.Errorf("composer check: no package matching %q under %s", opts.Target, root)
			tracker.Finish(err)
			return err
		}
	}

	var results []CheckResult
	failed := 0
	for _, p := range pkgs {
		r := runCheckOne(root, p)
		if !r.OK {
			failed++
		}
		results = append(results, r)
	}
	tracker.SetSummary(map[string]any{"packages": len(results), "failed": failed})

	cw := &calllog.Counter{W: w}
	renderErr := renderCheck(cw, opts.Format, results, failed)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

func filterTarget(pkgs []Pkg, target string) []Pkg {
	var out []Pkg
	for _, p := range pkgs {
		if p.Name == target || strings.HasSuffix(p.Name, "/"+target) || filepath.Base(p.Dir) == target {
			out = append(out, p)
		}
	}
	return out
}

func runCheckOne(root string, p Pkg) CheckResult {
	r := CheckResult{Name: orDefault(p.Name, p.Dir), Dir: p.Dir}
	if !contains(p.Scripts, "check") {
		r.Fail = "no 'check' script"
		return r
	}

	cmd := exec.Command("composer", "check", "--no-interaction")
	cmd.Dir = filepath.Join(root, p.Dir)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	start := time.Now()
	err := cmd.Run()
	r.DurationMs = time.Since(start).Milliseconds()

	if err == nil {
		r.OK = true
		return r
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.Exit = ee.ExitCode()
	} else {
		r.Exit = -1
		r.Fail = err.Error()
		return r
	}
	r.Fail = firstFailLine(buf.String())
	return r
}

// firstFailLine returns the first output line that looks like a failure, else
// the last non-empty line, truncated for compactness.
func firstFailLine(out string) string {
	lines := strings.Split(out, "\n")
	last := ""
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		last = t
		if failLineRe.MatchString(t) {
			return truncate(t, 200)
		}
	}
	return truncate(last, 200)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

var checkFields = []string{"pkg", "status", "exit", "dur_ms", "fail"}

func renderCheck(w io.Writer, format string, results []CheckResult, failed int) error {
	switch format {
	case "", "toon":
		fmt.Fprintf(w, "check[%d]{%s}: # failed=%d\n", len(results), strings.Join(checkFields, ","), failed)
		for _, r := range results {
			fmt.Fprintf(w, "%s%s,%s,%d,%d,%s\n",
				toon.Indent,
				toon.Scalar(r.Name),
				status(r),
				r.Exit,
				r.DurationMs,
				toon.Scalar(orDash(r.Fail)),
			)
		}
		return nil
	case "md":
		fmt.Fprintf(w, "# composer check (%d packages, %d failed)\n\n", len(results), failed)
		fmt.Fprintln(w, "| Package | Status | Exit | Time | Failure |")
		fmt.Fprintln(w, "| --- | --- | ---: | ---: | --- |")
		for _, r := range results {
			fmt.Fprintf(w, "| %s | %s | %d | %dms | %s |\n",
				r.Name, status(r), r.Exit, r.DurationMs, orDash(r.Fail))
		}
		return nil
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(results)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}

func status(r CheckResult) string {
	if r.OK {
		return "ok"
	}
	if r.Exit == 0 && r.Fail != "" {
		return "skip"
	}
	return "FAIL"
}
