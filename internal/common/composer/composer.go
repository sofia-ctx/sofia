// Package composer implements `sf composer` — compact, TOON-first views over a
// tree of PHP packages described by composer.json files. It replaces the
// recurring, token-expensive pattern of `cat`-ing each package's composer.json
// (plus `git tag` and grepping phpstan.neon) just to learn the shape of a
// monorepo or a collection of sibling library repos.
//
//	sf composer ls [root]      one digest row per package across a tree
//	sf composer show <pkg>     full metadata for a single package
//	sf composer check [pkg]    run each package's own quality gate, summarised
package composer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/gitexec"
	"github.com/sofia-ctx/sofia/internal/toon"
	"github.com/sofia-ctx/sofia/internal/walker"
)

// Options controls a `composer ls` run.
type Options struct {
	Root   string // tree to scan (default: cwd)
	Format string
}

// Pkg is one package's digest, as shown by `composer ls`.
type Pkg struct {
	Name       string   `json:"name"`
	Dir        string   `json:"dir"` // relative to scan root
	Version    string   `json:"version"`
	Type       string   `json:"type"`
	PHP        string   `json:"php"`
	PHPStan    string   `json:"phpstan"`
	Namespace  string   `json:"namespace,omitempty"`
	Scripts    []string `json:"scripts,omitempty"`
	Require    []string `json:"require,omitempty"`
	RequireDev []string `json:"require_dev,omitempty"`
}

// composerJSON is the subset of composer.json we read.
type composerJSON struct {
	Name       string                     `json:"name"`
	Type       string                     `json:"type"`
	Require    map[string]string          `json:"require"`
	RequireDev map[string]string          `json:"require-dev"`
	Scripts    map[string]json.RawMessage `json:"scripts"`
	Autoload   struct {
		PSR4 map[string]json.RawMessage `json:"psr-4"`
	} `json:"autoload"`
}

var levelRe = regexp.MustCompile(`(?m)^\s*level:\s*(\w+)`)

// Run collects the digest, renders it, and logs the call.
func Run(opts Options, w io.Writer) error {
	root := opts.Root
	if root == "" {
		root = "."
	}
	tracker := calllog.Start("composer ls", []string{"--format=" + opts.Format, root})

	pkgs, err := Collect(root)
	if err != nil {
		tracker.Finish(err)
		return err
	}
	tracker.SetSummary(map[string]any{"packages": len(pkgs), "root": root})

	cw := &calllog.Counter{W: w}
	renderErr := render(cw, opts.Format, pkgs)
	tracker.RecordOutput(cw)
	tracker.Finish(renderErr)
	return renderErr
}

// Collect walks root for composer.json files and returns one Pkg each, sorted
// by package name (then dir for unnamed packages).
func Collect(root string) ([]Pkg, error) {
	files, errs := walker.Files(walker.Options{
		Root:       root,
		IgnoreDirs: map[string]bool{"vendor": true, "node_modules": true, ".git": true},
		Exts:       map[string]bool{".json": true},
	})

	var pkgs []Pkg
	for path := range files {
		if filepath.Base(path) != "composer.json" {
			continue
		}
		p, err := parsePkg(root, path)
		if err != nil {
			continue // skip unreadable/invalid composer.json rather than abort
		}
		pkgs = append(pkgs, p)
	}
	if err := <-errs; err != nil {
		return nil, err
	}

	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Name != pkgs[j].Name {
			return pkgs[i].Name < pkgs[j].Name
		}
		return pkgs[i].Dir < pkgs[j].Dir
	})
	return pkgs, nil
}

// parsePkg reads and decodes one composer.json into a Pkg.
func parsePkg(root, path string) (Pkg, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Pkg{}, err
	}
	var cj composerJSON
	if err := json.Unmarshal(data, &cj); err != nil {
		return Pkg{}, err
	}

	dir := filepath.Dir(path)

	p := Pkg{
		Name:       cj.Name,
		Dir:        relDir(root, dir),
		Version:    gitLatestTag(dir),
		Type:       orDefault(cj.Type, "library"),
		PHP:        cj.Require["php"],
		PHPStan:    phpstanLevel(dir),
		Namespace:  strings.Join(sortedKeys(cj.Autoload.PSR4), "|"),
		Scripts:    sortedScriptKeys(cj.Scripts),
		Require:    realDeps(cj.Require),
		RequireDev: realDeps(cj.RequireDev),
	}
	return p, nil
}

// realDeps returns the non-php, non-ext keys of a require/require-dev map,
// sorted — the actual package dependencies, dropping the platform constraints.
func realDeps(require map[string]string) []string {
	var out []string
	for k := range require {
		if k == "php" || strings.HasPrefix(k, "ext-") {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedScriptKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// relDir renders dir relative to root for display. It tolerates an
// absolute/relative mismatch (e.g. an absolute --root paired with a
// cwd-relative match from `composer show`) by normalising both to absolute
// before relating, and falls back to the basename when they share no base.
func relDir(root, dir string) string {
	ar, e1 := filepath.Abs(root)
	ad, e2 := filepath.Abs(dir)
	if e1 == nil && e2 == nil {
		if rel, err := filepath.Rel(ar, ad); err == nil {
			if rel == "" || rel == "." {
				return "."
			}
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(dir)
}

// gitLatestTag returns the most recent tag reachable from the package's HEAD,
// or "" when the dir is not a repo / has no tags. composer.json carries no
// `version` field by house rule, so the tag is the version of record.
func gitLatestTag(dir string) string {
	out, err := gitexec.Run(dir, "describe", "--tags", "--abbrev=0")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// phpstanLevel reads the configured PHPStan level from phpstan.neon[.dist],
// or "" when absent.
func phpstanLevel(dir string) string {
	for _, name := range []string{"phpstan.neon", "phpstan.neon.dist"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if m := levelRe.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	return ""
}

var lsFields = []string{"pkg", "version", "type", "php", "phpstan", "scripts", "deps", "dev"}

func render(w io.Writer, format string, pkgs []Pkg) error {
	switch format {
	case "", "toon":
		return renderTOON(w, pkgs)
	case "md":
		return renderMarkdown(w, pkgs)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(pkgs)
	default:
		return fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
}

func renderTOON(w io.Writer, pkgs []Pkg) error {
	fmt.Fprintf(w, "packages[%d]{%s}:\n", len(pkgs), strings.Join(lsFields, ","))
	for _, p := range pkgs {
		fmt.Fprintf(w, "%s%s,%s,%s,%s,%s,%s,%s,%s\n",
			toon.Indent,
			toon.Scalar(orDefault(p.Name, p.Dir)),
			toon.Scalar(orDash(p.Version)),
			toon.Scalar(p.Type),
			toon.Scalar(orDash(p.PHP)),
			toon.Scalar(orDash(p.PHPStan)),
			toon.Scalar(orDash(strings.Join(p.Scripts, "|"))),
			toon.Scalar(orDash(strings.Join(p.Require, "|"))),
			toon.Scalar(orDash(strings.Join(p.RequireDev, "|"))),
		)
	}
	return nil
}

func renderMarkdown(w io.Writer, pkgs []Pkg) error {
	fmt.Fprintf(w, "# composer packages (%d)\n\n", len(pkgs))
	fmt.Fprintln(w, "| Package | Version | Type | PHP | PHPStan | Scripts | Deps | Dev deps |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | ---: | --- | --- | --- |")
	for _, p := range pkgs {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			orDefault(p.Name, p.Dir), orDash(p.Version), p.Type, orDash(p.PHP),
			orDash(p.PHPStan), orDash(strings.Join(p.Scripts, " ")),
			orDash(strings.Join(p.Require, " ")), orDash(strings.Join(p.RequireDev, " ")))
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
