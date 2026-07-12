// Package launch implements `sf claude` — a thin, generic launcher that
// starts the `claude` CLI for a project. Binding to the project's shared
// instructions is implicit: claude runs with cwd set to the project dir, so it
// loads that project's own root AGENTS.md/CLAUDE.md natively. On top of that,
// sf injects an optional personal *overlay* — per-project instructions kept in
// a private repo (see overlay.go) — via --append-system-prompt, so it takes
// precedence over the repo's AGENTS.md. The launcher's job is the mechanics:
// resolve the project dir, assemble claude's argv, and hand off to the process
// (interactive) or run it headlessly (--task), propagating its exit code.
package launch

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Target is a resolved launch target: the project's working dir plus the
// name it's tagged with (passed to claude as `-n` and stamped into
// SOFIA_TAG). Name is fixed at resolution time and untouched by a fork
// selector, so `sf <proj> <tool>` calls made from inside a forked worktree
// still attribute to the main project.
type Target struct {
	Name string
	Dir  string // claude's working dir (cwd) — may be a fork dir
}

// Options carries everything resolved from flags before launch.
type Options struct {
	Model      string
	Effort     string
	Permission string
	Task       string
	Out        string
	JSON       bool
	Quiet      bool
	DryRun     bool
	NoOverlay  bool     // skip the personal overlay injection
	Extra      []string // passthrough args (after `--`)
}

// NotFoundError is returned when a bare project name matched nothing obvious:
// no saved alias, no existing $SF_CLAUDE_DIR child, and no ./ or ../ sibling.
// It's a distinct type so the command layer can offer a deep search before
// giving up; on its own its message is actionable.
type NotFoundError struct {
	Name        string
	SFClaudeDir string // $SF_CLAUDE_DIR at resolution time ("" if unset)
}

func (e *NotFoundError) Error() string {
	tried := make([]string, 0, 3)
	if e.SFClaudeDir != "" {
		tried = append(tried, filepath.Join(e.SFClaudeDir, e.Name)+" (under $SF_CLAUDE_DIR)")
	}
	tried = append(tried, "./"+e.Name, "../"+e.Name)
	msg := fmt.Sprintf("project %q not found: no %s", e.Name, strings.Join(tried, ", no "))
	if e.SFClaudeDir == "" {
		return msg + "; set $SF_CLAUDE_DIR to your projects root, or pass --dir <path>"
	}
	return msg + "; pass --dir <path>"
}

// ResolveDir resolves the project's working dir. Order: an explicit --dir
// override; else, given a bare project name, in turn — a saved alias (see the
// alias store), an existing $SF_CLAUDE_DIR/<project>, then the name next to the
// current directory (a child ./<project> or a sibling ../<project>, the latter
// also matching the current dir itself when it's named <project>). If none
// match it returns a *NotFoundError (the command layer may then deep-search).
// With no name at all, the current directory. The result is validated to exist
// and be a directory, and returned absolute.
func ResolveDir(dirFlag, project string) (string, error) {
	var dir string
	switch {
	case dirFlag != "":
		dir = dirFlag
	case project != "":
		if d, ok := loadAliases()[project]; ok && dirExists(d) {
			dir = d
			break
		}
		if root := os.Getenv("SF_CLAUDE_DIR"); root != "" {
			if cand := filepath.Join(root, project); dirExists(cand) {
				dir = cand
				break
			}
		}
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		switch {
		case dirExists(filepath.Join(wd, project)):
			dir = filepath.Join(wd, project)
		case dirExists(filepath.Join(wd, "..", project)):
			dir = filepath.Join(wd, "..", project)
		default:
			return "", &NotFoundError{Name: project, SFClaudeDir: os.Getenv("SF_CLAUDE_DIR")}
		}
	default:
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if !dirExists(abs) {
		return "", fmt.Errorf("not a directory: %s", abs)
	}
	return abs, nil
}

// ResolveTarget resolves the project dir (see ResolveDir) and names it after
// the resolved dir's basename.
func ResolveTarget(dirFlag, project string) (Target, error) {
	dir, err := ResolveDir(dirFlag, project)
	if err != nil {
		return Target{}, err
	}
	return Target{Name: filepath.Base(dir), Dir: dir}, nil
}

// systemPromptFromEnv reads $SF_CLAUDE_PROMPT_FILE and returns its contents,
// or "" if the var is unset or the file can't be read — there's no
// hardcoded fallback prompt; silence just means claude gets none.
func systemPromptFromEnv() string {
	p := os.Getenv("SF_CLAUDE_PROMPT_FILE")
	if p == "" {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(data)
}

// baseArgs are the claude flags common to both interactive and task modes.
// System-prompt text comes from two sources, joined into one
// --append-system-prompt: the $SF_CLAUDE_PROMPT_FILE global (if any), then the
// project's personal overlay (if any) — the overlay is added last and carries
// its own precedence preamble. The overlay's dir is also --add-dir'd so the
// session can edit it, and when the overlay is a Claude Code plugin it's loaded
// with --plugin-dir so its slash-commands are available for the session.
func baseArgs(t Target, o Options) []string {
	args := []string{"--add-dir", t.Dir, "-n", t.Name}

	prompts := make([]string, 0, 2)
	if prompt := systemPromptFromEnv(); prompt != "" {
		prompts = append(prompts, prompt)
	}
	if !o.NoOverlay {
		if ov, ok := resolveOverlay(t.Name); ok {
			args = append(args, "--add-dir", ov.dir)
			if ov.plugin {
				args = append(args, "--plugin-dir", ov.dir)
			}
			if ov.agents != "" {
				if prompt := overlayPrompt(ov.dir, ov.agents); prompt != "" {
					prompts = append(prompts, prompt)
				}
			}
		}
	}
	if len(prompts) > 0 {
		args = append(args, "--append-system-prompt", strings.Join(prompts, "\n\n"))
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	if o.Permission != "" {
		args = append(args, "--permission-mode", o.Permission)
	}
	return args
}

// InteractiveArgs assembles the claude argv for an interactive session.
func InteractiveArgs(t Target, o Options) []string {
	return append(baseArgs(t, o), o.Extra...)
}

// TaskArgs assembles the claude argv for a one-shot `-p` task run.
func TaskArgs(t Target, o Options) []string {
	args := []string{"-p", o.Task}
	args = append(args, baseArgs(t, o)...)
	if o.JSON {
		args = append(args, "--output-format", "json")
	}
	return append(args, o.Extra...)
}

// Run dispatches to dry-run / task / interactive. For task mode it returns
// claude's exit code; interactive mode replaces the process (unix) and never
// returns on success.
func Run(t Target, o Options, stdout io.Writer) (int, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return 1, fmt.Errorf("claude not found in PATH: %w", err)
	}

	if o.Task != "" {
		args := TaskArgs(t, o)
		if o.DryRun {
			printDry(stdout, t, args)
			return 0, nil
		}
		return runTask(bin, args, t, o.Out, o.Quiet, stdout)
	}

	args := InteractiveArgs(t, o)
	if o.DryRun {
		printDry(stdout, t, args)
		return 0, nil
	}
	if err := os.Chdir(t.Dir); err != nil {
		return 1, err
	}
	// Hand the terminal to claude by replacing this process. Stamp the
	// project name so every `sf` call this session spawns (claude → bash →
	// sf) attributes to the right project via SOFIA_TAG, even when SOFIA_TAG
	// alone (not cwd) is authoritative — e.g. a forked worktree dir.
	return 0, interactiveExec(bin, append([]string{"claude"}, args...), childEnv(t))
}

// runTask runs claude -p and routes its stdout per --out/--quiet:
//   - no --out:      result → stdout
//   - --out:         result → stdout AND file (tee)
//   - --out --quiet: result → file only; stdout silent (exit code only)
//
// Returns claude's exit code.
func runTask(bin string, args []string, t Target, out string, quiet bool, stdout io.Writer) (int, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.Dir
	cmd.Env = childEnv(t)
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	var f *os.File
	switch {
	case out != "":
		var err error
		if f, err = os.Create(out); err != nil {
			return 1, err
		}
		if quiet {
			cmd.Stdout = f
		} else {
			cmd.Stdout = io.MultiWriter(stdout, f)
		}
	default:
		cmd.Stdout = stdout
	}

	runErr := cmd.Run()
	if f != nil {
		_ = f.Close()
		if !quiet {
			fmt.Fprintf(os.Stderr, "saved: %s\n", out)
		}
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return ee.ExitCode(), nil
		}
		return 1, runErr
	}
	return 0, nil
}

// printDry prints the resolved command without running it: the cwd, the env
// delta this launch stamps, and the argv — eliding a long system-prompt
// value inline (printed separately) for readability.
func printDry(w io.Writer, t Target, args []string) {
	fmt.Fprintf(w, "cd %s\n", t.Dir)
	fmt.Fprintf(w, "env: SOFIA_TAG=%s SOFIA_PROJECT_ROOT=%s\n", t.Name, t.Dir)
	// Surface an applied overlay (the second --add-dir) on its own line.
	for i, seen := 0, 0; i < len(args)-1; i++ {
		if args[i] == "--add-dir" {
			if seen++; seen == 2 {
				fmt.Fprintf(w, "overlay: %s\n", args[i+1])
			}
		}
	}
	fmt.Fprint(w, "claude")
	prompt := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--append-system-prompt" && i+1 < len(args) {
			fmt.Fprint(w, " --append-system-prompt <prompt>")
			prompt = args[i+1]
			i++
			continue
		}
		if strings.ContainsAny(a, " \t") {
			fmt.Fprintf(w, " %q", a)
		} else {
			fmt.Fprintf(w, " %s", a)
		}
	}
	fmt.Fprintln(w)
	if prompt != "" {
		fmt.Fprintf(w, "--- prompt ---\n%s\n", prompt)
	}
}

// childEnv is the process environment for the launched claude: the current
// env plus SOFIA_TAG=<name> and SOFIA_PROJECT_ROOT=<dir>, so every `sf`
// call the session spawns targets this launch's project/dir without the
// agent needing `cd` or a `--root` flag.
func childEnv(t Target) []string {
	return append(os.Environ(), "SOFIA_TAG="+t.Name, "SOFIA_PROJECT_ROOT="+t.Dir)
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
