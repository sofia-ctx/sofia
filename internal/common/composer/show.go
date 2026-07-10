package composer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/pkg/toon"
	"github.com/sofia-ctx/sofia/pkg/walker"
)

// ShowOptions controls a `composer show` run.
type ShowOptions struct {
	Root   string // tree to search (default: cwd)
	Target string // package name, name suffix, dir basename, or path
	Format string
}

type kv struct {
	K string `json:"name"`
	V string `json:"value"`
}

// Detail is the full single-package view shown by `composer show`.
type Detail struct {
	Name       string `json:"name"`
	Dir        string `json:"dir"`
	Version    string `json:"version"`
	Type       string `json:"type"`
	PHP        string `json:"php"`
	PHPStan    string `json:"phpstan"`
	Namespace  string `json:"namespace,omitempty"`
	Scripts    []kv   `json:"scripts,omitempty"`
	Require    []kv   `json:"require,omitempty"`
	RequireDev []kv   `json:"require_dev,omitempty"`
}

// RunShow resolves a single package, renders its detail, and logs the call.
func RunShow(opts ShowOptions, w io.Writer) error {
	root := opts.Root
	if root == "" {
		root = "."
	}
	tracker := calllog.Start("composer show", []string{opts.Target, "--format=" + opts.Format})

	d, err := CollectOne(root, opts.Target)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"package": d.Name, "dir": d.Dir})

	cw := &calllog.Counter{W: w}
	renderErr := renderDetail(cw, opts.Format, d)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// CollectOne finds the package matching target under root and returns its Detail.
func CollectOne(root, target string) (Detail, error) {
	path, err := findOne(root, target)
	if err != nil {
		return Detail{}, err
	}
	return parseDetail(root, path)
}

// findOne locates the composer.json for target. Resolution: an explicit path
// (file or dir), then a tree scan matching exact name, name suffix `/target`,
// or the package directory's basename.
func findOne(root, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("composer show: package name or path required")
	}
	// Explicit path: a composer.json, or a dir holding one.
	if st, err := os.Stat(target); err == nil {
		if st.IsDir() {
			cand := filepath.Join(target, "composer.json")
			if _, err := os.Stat(cand); err == nil {
				return cand, nil
			}
		} else if filepath.Base(target) == "composer.json" {
			return target, nil
		}
	}

	files, errs := walker.Files(walker.Options{
		Root:       root,
		IgnoreDirs: map[string]bool{"vendor": true, "node_modules": true, ".git": true},
		Exts:       map[string]bool{".json": true},
	})
	var byName, byDir string
	for path := range files {
		if filepath.Base(path) != "composer.json" {
			continue
		}
		name := composerName(path)
		switch {
		case name == target, strings.HasSuffix(name, "/"+target):
			byName = path
		case filepath.Base(filepath.Dir(path)) == target:
			if byDir == "" {
				byDir = path
			}
		}
		if byName != "" {
			break
		}
	}
	if err := <-errs; err != nil {
		return "", err
	}
	switch {
	case byName != "":
		return byName, nil
	case byDir != "":
		return byDir, nil
	default:
		return "", fmt.Errorf("composer show: no package matching %q under %s", target, root)
	}
}

func composerName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cj struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &cj)
	return cj.Name
}

func parseDetail(root, path string) (Detail, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Detail{}, err
	}
	var cj composerJSON
	if err := json.Unmarshal(data, &cj); err != nil {
		return Detail{}, err
	}

	dir := filepath.Dir(path)

	d := Detail{
		Name:       cj.Name,
		Dir:        relDir(root, dir),
		Version:    gitLatestTag(dir),
		Type:       orDefault(cj.Type, "library"),
		PHP:        cj.Require["php"],
		PHPStan:    phpstanLevel(dir),
		Namespace:  strings.Join(sortedKeys(cj.Autoload.PSR4), "|"),
		Scripts:    scriptKVs(cj.Scripts),
		Require:    depKVs(cj.Require),
		RequireDev: depKVs(cj.RequireDev),
	}
	return d, nil
}

// scriptKVs renders each composer script as name→command, flattening an array
// of steps into ` && `-joined text.
func scriptKVs(m map[string]json.RawMessage) []kv {
	out := make([]kv, 0, len(m))
	for k, raw := range m {
		out = append(out, kv{K: k, V: scriptValue(raw)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].K < out[j].K })
	return out
}

func scriptValue(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, " && ")
	}
	return strings.TrimSpace(string(raw))
}

func depKVs(m map[string]string) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{K: k, V: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].K < out[j].K })
	return out
}

func renderDetail(w io.Writer, format string, d Detail) error {
	switch format {
	case "", "toon":
		return renderDetailTOON(w, d)
	case "md":
		return renderDetailMarkdown(w, d)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(d)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}

func renderDetailTOON(w io.Writer, d Detail) error {
	fmt.Fprintf(w, "package: %s\n", orDash(d.Name))
	fmt.Fprintf(w, "dir: %s\n", d.Dir)
	fmt.Fprintf(w, "version: %s\n", orDash(d.Version))
	fmt.Fprintf(w, "type: %s\n", d.Type)
	fmt.Fprintf(w, "php: %s\n", orDash(d.PHP))
	fmt.Fprintf(w, "phpstan: %s\n", orDash(d.PHPStan))
	if d.Namespace != "" {
		fmt.Fprintf(w, "namespace: %s\n", d.Namespace)
	}
	writeKVBlock(w, "scripts", []string{"name", "cmd"}, d.Scripts)
	writeKVBlock(w, "require", []string{"pkg", "constraint"}, d.Require)
	writeKVBlock(w, "require_dev", []string{"pkg", "constraint"}, d.RequireDev)
	return nil
}

func writeKVBlock(w io.Writer, label string, cols []string, items []kv) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "%s[%d]{%s}:\n", label, len(items), strings.Join(cols, ","))
	for _, it := range items {
		fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(it.K), toon.Scalar(it.V))
	}
}

func renderDetailMarkdown(w io.Writer, d Detail) error {
	fmt.Fprintf(w, "# %s\n\n", orDash(d.Name))
	fmt.Fprintf(w, "- dir: `%s`\n- version: %s\n- type: %s\n- php: %s\n- phpstan: %s\n",
		d.Dir, orDash(d.Version), d.Type, orDash(d.PHP), orDash(d.PHPStan))
	if d.Namespace != "" {
		fmt.Fprintf(w, "- namespace: `%s`\n", d.Namespace)
	}
	mdKVList(w, "Scripts", d.Scripts)
	mdKVList(w, "Require", d.Require)
	mdKVList(w, "Require-dev", d.RequireDev)
	return nil
}

func mdKVList(w io.Writer, title string, items []kv) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "\n## %s\n", title)
	for _, it := range items {
		fmt.Fprintf(w, "- `%s`: %s\n", it.K, it.V)
	}
}
