package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sofia-ctx/sofia/internal/gitclone"
	"github.com/sofia-ctx/sofia/internal/plugin"
)

// InstallOptions configures Install. Project defaults to the current working
// directory, resolved to an absolute path once at the top of Install so a
// relative --project is never re-resolved against a later os.Chdir.
type InstallOptions struct {
	Src     string // git URL or local directory
	Ref     string // branch or tag; git sources only
	Project string // target project root; "" defaults to cwd
	Force   bool   // skip the conflict check and overwrite
}

// Result summarizes what Install actually wrote, for the CLI to report.
type Result struct {
	Name    string
	Source  Source
	Plugins []string
	Files   int // claude + project files written
}

// plannedFile is one concrete file the install plan will write: Src is the
// absolute path to read content from, Perm preserves the source's own file
// mode (so an executable script inside a claude skill stays executable), and
// Dest is either an absolute path (claude files, which live outside any
// project) or a project-relative path (project files, resolved against
// InstallOptions.Project at apply/conflict-check time).
type plannedFile struct {
	Src  string
	Dest string
	Perm os.FileMode
}

// Install resolves opts.Src (a git URL or a local directory), validates its
// pack.yaml, and lays out every artifact it declares — but only after
// checking the whole plan for conflicts, so a bad install never applies half
// of itself. See pack.go's package doc for the full two-phase contract.
func Install(opts InstallOptions) (Result, error) {
	project, err := resolveProject(opts.Project)
	if err != nil {
		return Result{}, err
	}

	packRoot, source, cleanup, err := resolveSource(opts.Src, opts.Ref)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(packRoot, manifestFile))
	if err != nil {
		return Result{}, fmt.Errorf("%s has no %s", opts.Src, manifestFile)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return Result{}, err
	}
	if err := m.Validate(); err != nil {
		return Result{}, err
	}

	existing, _, err := loadReceipt(m.Name)
	if err != nil {
		return Result{}, err
	}

	claudeFiles, err := planClaudeFiles(packRoot, m.Claude)
	if err != nil {
		return Result{}, err
	}
	projectFiles, err := planProjectFiles(packRoot, m.Instructions, m.Templates)
	if err != nil {
		return Result{}, err
	}

	if !opts.Force {
		if cs := findConflicts(existing, claudeFiles, projectFiles, project); len(cs) > 0 {
			return Result{}, conflictError(cs)
		}
	}

	// (a) plugins, then exactly one cache refresh — never one per plugin, or
	// `sf plugin list` would go stale mid-batch.
	var installedPlugins []string
	for _, p := range m.Plugins {
		var name string
		var err error
		if p.Path != "" {
			name, err = plugin.Install(filepath.Join(packRoot, p.Path))
		} else {
			name, err = plugin.InstallFromGit(p.Git, p.Ref)
		}
		if err != nil {
			return Result{}, fmt.Errorf("pack %s: plugin: %w", m.Name, err)
		}
		installedPlugins = append(installedPlugins, name)
	}
	if len(m.Plugins) > 0 {
		if _, err := plugin.Update(); err != nil {
			return Result{}, err
		}
	}

	// (b) claude files.
	claudeReceipt := make([]ClaudeFile, 0, len(claudeFiles))
	for _, f := range claudeFiles {
		if err := writeFile(f, f.Dest); err != nil {
			return Result{}, err
		}
		sha, err := sha256File(f.Src)
		if err != nil {
			return Result{}, err
		}
		claudeReceipt = append(claudeReceipt, ClaudeFile{Dest: f.Dest, SHA256: sha})
	}

	// (c) project files.
	projectReceipt := make([]ProjectFile, 0, len(projectFiles))
	for _, f := range projectFiles {
		abs := filepath.Join(project, f.Dest)
		if err := writeFile(f, abs); err != nil {
			return Result{}, err
		}
		sha, err := sha256File(f.Src)
		if err != nil {
			return Result{}, err
		}
		projectReceipt = append(projectReceipt, ProjectFile{Dest: filepath.ToSlash(f.Dest), SHA256: sha})
	}

	// (d) canonical copy — the source of truth `sf pack list`/`info` read.
	if err := os.RemoveAll(canonDir(m.Name)); err != nil {
		return Result{}, err
	}
	if err := copyTree(packRoot, canonDir(m.Name)); err != nil {
		return Result{}, err
	}

	// (e) receipt: this project's entry is replaced; plugins/claude/source
	// are replaced wholesale (they describe the pack's *global* footprint,
	// which a reinstall from any project recomputes in full).
	now := time.Now().UTC()
	existing.Version = ReceiptVersion
	existing.Name = m.Name
	existing.Source = source
	existing.InstalledAt = now
	existing.Plugins = installedPlugins
	existing.Claude = claudeReceipt
	if existing.Projects == nil {
		existing.Projects = map[string]ProjectInstall{}
	}
	existing.Projects[project] = ProjectInstall{InstalledAt: now, Files: projectReceipt}
	if err := saveReceipt(existing); err != nil {
		return Result{}, err
	}

	return Result{
		Name:    m.Name,
		Source:  source,
		Plugins: installedPlugins,
		Files:   len(claudeReceipt) + len(projectReceipt),
	}, nil
}

// resolveProject resolves the target project root: opts.Project if set, else
// the current working directory, always as an absolute path (it becomes a
// receipt map key and must be stable regardless of a later chdir).
func resolveProject(project string) (string, error) {
	if project == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		project = wd
	}
	return filepath.Abs(project)
}

// resolveSource turns Src into a local pack root ready to read pack.yaml
// from, plus the Source the receipt should record. A git URL is
// shallow-cloned into a temp directory (cleanup removes it); a local
// directory is used in place (cleanup is a no-op) and the receipt records its
// absolute path.
func resolveSource(src, ref string) (root string, source Source, cleanup func(), err error) {
	noop := func() {}
	if gitclone.IsURL(src) {
		tmp, err := os.MkdirTemp("", "sofia-pack-install-*")
		if err != nil {
			return "", Source{}, noop, err
		}
		cleanup = func() { _ = os.RemoveAll(tmp) }
		dst := filepath.Join(tmp, "pack")
		commit, err := gitclone.CloneShallow(src, ref, dst)
		if err != nil {
			cleanup()
			return "", Source{}, noop, err
		}
		return dst, Source{URL: src, Ref: ref, Commit: commit}, cleanup, nil
	}
	if ref != "" {
		return "", Source{}, noop, fmt.Errorf("--ref only applies to a git URL, not a local directory")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", Source{}, noop, err
	}
	if !info.IsDir() {
		return "", Source{}, noop, fmt.Errorf("%s is not a directory", src)
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", Source{}, noop, err
	}
	return abs, Source{Path: abs}, noop, nil
}

// planClaudeFiles expands a manifest's claude block into concrete writes.
// Skills land under $CLAUDE_DIR/skills/<basename(src)>/, commands directly
// under $CLAUDE_DIR/commands/ — dest is never author-settable for either.
func planClaudeFiles(packRoot string, c Claude) ([]plannedFile, error) {
	var out []plannedFile
	for _, fm := range c.Skills {
		fs, err := expand(packRoot, fm.Src, filepath.Join(claudeDir(), "skills"))
		if err != nil {
			return nil, err
		}
		out = append(out, fs...)
	}
	for _, fm := range c.Commands {
		fs, err := expand(packRoot, fm.Src, filepath.Join(claudeDir(), "commands"))
		if err != nil {
			return nil, err
		}
		out = append(out, fs...)
	}
	return out, nil
}

// planProjectFiles expands instructions and templates into concrete writes,
// both landing at the target project's root — they're shaped identically,
// only the manifest section they came from differs.
func planProjectFiles(packRoot string, instructions, templates []FileMap) ([]plannedFile, error) {
	var out []plannedFile
	for _, fm := range instructions {
		fs, err := expandFileMap(packRoot, fm)
		if err != nil {
			return nil, err
		}
		out = append(out, fs...)
	}
	for _, fm := range templates {
		fs, err := expandFileMap(packRoot, fm)
		if err != nil {
			return nil, err
		}
		out = append(out, fs...)
	}
	return out, nil
}

// expand expands one claude FileMap into concrete files rooted at destRoot: a
// single file src lands directly at destRoot/<basename(src)>; a directory
// src's contents land under destRoot/<basename(src)>/..., mirroring their
// tree. WalkDir's own rel components are safe by construction (they come from
// directory listings under abs, not from manifest strings), so only the
// top-level src needed the safeRel guard Validate already ran.
func expand(packRoot, src, destRoot string) ([]plannedFile, error) {
	abs := filepath.Join(packRoot, src)
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	base := filepath.Base(filepath.Clean(src))
	if !info.IsDir() {
		return []plannedFile{{Src: abs, Dest: filepath.Join(destRoot, base), Perm: info.Mode().Perm()}}, nil
	}
	var out []plannedFile
	err = filepath.WalkDir(abs, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		out = append(out, plannedFile{Src: path, Dest: filepath.Join(destRoot, base, rel), Perm: info.Mode().Perm()})
		return nil
	})
	return out, err
}

// expandFileMap expands one instructions/templates FileMap into concrete
// project files: src (file or directory) relative to the pack root, landing
// at fm.Dest (default filepath.Base(fm.Src)) relative to the project root.
func expandFileMap(packRoot string, fm FileMap) ([]plannedFile, error) {
	abs := filepath.Join(packRoot, fm.Src)
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fm.Src, err)
	}
	dest := fm.Dest
	if dest == "" {
		dest = filepath.Base(filepath.Clean(fm.Src))
	}
	if !info.IsDir() {
		return []plannedFile{{Src: abs, Dest: dest, Perm: info.Mode().Perm()}}, nil
	}
	var out []plannedFile
	err = filepath.WalkDir(abs, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		out = append(out, plannedFile{Src: path, Dest: filepath.Join(dest, rel), Perm: info.Mode().Perm()})
		return nil
	})
	return out, err
}

// conflict describes one destination the install plan cannot silently
// overwrite: something is there that this pack didn't put there, or that was
// hand-edited since.
type conflict struct {
	display string
	reason  string
}

// findConflicts checks every planned write against what's already on disk. A
// destination is safe to (re)write when it doesn't exist yet, already holds
// exactly the planned content, or holds exactly what the receipt last
// recorded there (a normal reinstall-with-updated-source); anything else — an
// untracked file in the way, or one edited since install — is a conflict.
// Every conflict is collected before Install writes a single byte.
func findConflicts(r Receipt, claudeFiles, projectFiles []plannedFile, project string) []conflict {
	recordedClaude := map[string]string{}
	for _, c := range r.Claude {
		recordedClaude[c.Dest] = c.SHA256
	}
	recordedProject := map[string]string{}
	if pi, ok := r.Projects[project]; ok {
		for _, f := range pi.Files {
			recordedProject[filepath.FromSlash(f.Dest)] = f.SHA256
		}
	}

	var conflicts []conflict
	check := func(abs string, f plannedFile, display string, recorded map[string]string, key string) {
		info, err := os.Stat(abs)
		if err != nil {
			return // missing → nothing in the way
		}
		if info.IsDir() {
			conflicts = append(conflicts, conflict{display: display, reason: "exists, not managed by sf"})
			return
		}
		currentSHA, err := sha256File(abs)
		if err != nil {
			conflicts = append(conflicts, conflict{display: display, reason: err.Error()})
			return
		}
		plannedSHA, err := sha256File(f.Src)
		if err != nil {
			conflicts = append(conflicts, conflict{display: display, reason: err.Error()})
			return
		}
		if currentSHA == plannedSHA {
			return // identical content — nothing to conflict over
		}
		if recSHA, ok := recorded[key]; ok && recSHA == currentSHA {
			return // untouched since install; safe to update
		}
		if _, ok := recorded[key]; ok {
			conflicts = append(conflicts, conflict{display: display, reason: "modified since install"})
		} else {
			conflicts = append(conflicts, conflict{display: display, reason: "exists, not managed by sf"})
		}
	}

	for _, f := range claudeFiles {
		check(f.Dest, f, f.Dest, recordedClaude, f.Dest)
	}
	for _, f := range projectFiles {
		abs := filepath.Join(project, f.Dest)
		check(abs, f, f.Dest, recordedProject, f.Dest)
	}
	return conflicts
}

// conflictError formats every collected conflict into the single error
// Install returns; main() prefixes it with "error: " so the final text reads
// exactly like:
//
//	error: 2 conflicts (rerun with --force to overwrite):
//	  AGENTS.md (exists, not managed by sf)
//	  .agents/backend.md (modified since install)
func conflictError(cs []conflict) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%d conflict", len(cs))
	if len(cs) != 1 {
		b.WriteString("s")
	}
	b.WriteString(" (rerun with --force to overwrite):")
	for _, c := range cs {
		fmt.Fprintf(&b, "\n  %s (%s)", c.display, c.reason)
	}
	return errors.New(b.String())
}

// claudeDir resolves Claude's config directory: $CLAUDE_DIR overrides it (the
// same override the repo's own Makefile honors, so hermetic tests and a real
// dev install agree), default ~/.claude otherwise.
func claudeDir() string {
	if d := os.Getenv("CLAUDE_DIR"); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".claude")
	}
	return ".claude"
}

// writeFile copies f.Src to destAbs, creating parent directories as needed
// and preserving f.Perm (so an executable script inside a claude skill stays
// executable).
func writeFile(f plannedFile, destAbs string) error {
	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(f.Src)
	if err != nil {
		return err
	}
	return os.WriteFile(destAbs, data, f.Perm)
}

// copyTree recursively copies src to dst, skipping any ".git" directory (the
// canon copy is a snapshot for `sf pack list`/reinstall, not a git clone) and
// preserving file permission bits. This is pack's own copy of
// plugin.copyTree (unexported there) plus the .git skip — duplicated rather
// than exported, the same call this codebase already made for
// dedup.sanitize (copied verbatim from hook.sanitize).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
