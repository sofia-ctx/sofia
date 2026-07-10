package launch

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestResolveDirExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	got, err := ResolveDir(dir, "irrelevant")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want, _ := filepath.Abs(dir)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDirProjectUnderSFClaudeDir(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "myproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_CLAUDE_DIR", root)

	got, err := ResolveDir("", "myproj")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if got != proj {
		t.Errorf("got %q, want %q", got, proj)
	}
}

func TestResolveDirProjectWithoutSFClaudeDir(t *testing.T) {
	t.Setenv("SF_CLAUDE_DIR", "")
	if _, err := ResolveDir("", "myproj"); err == nil {
		t.Fatal("expected error when $SF_CLAUDE_DIR is unset and a project name is given")
	} else if !strings.Contains(err.Error(), "SF_CLAUDE_DIR") || !strings.Contains(err.Error(), "--dir") {
		t.Errorf("error should mention $SF_CLAUDE_DIR and --dir: %v", err)
	}
}

func TestResolveDirDefaultsToCwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got, err := ResolveDir("", "")
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want, _ := filepath.Abs(dir)
	// Resolve symlinks on both sides — macOS temp dirs live under /var, a
	// symlink to /private/var, and cwd resolution follows it.
	wantReal, _ := filepath.EvalSymlinks(want)
	gotReal, _ := filepath.EvalSymlinks(got)
	if gotReal != wantReal {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDirNotADirectory(t *testing.T) {
	if _, err := ResolveDir("/no/such/dir/at/all", ""); err == nil {
		t.Error("expected error for a missing dir")
	}
}

func TestResolveTargetNames(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "myproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	target, err := ResolveTarget(proj, "")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Name != "myproj" || target.Dir != proj {
		t.Errorf("target = %+v, want Name=myproj Dir=%s", target, proj)
	}
}

func TestBaseArgs(t *testing.T) {
	target := Target{Name: "myproj", Dir: "/w/myproj"}

	ia := InteractiveArgs(target, Options{Model: "opus", Permission: "plan", Extra: []string{"--", "-c"}})
	js := strings.Join(ia, " ")
	for _, want := range []string{"--add-dir /w/myproj", "-n myproj", "--model opus", "--permission-mode plan", "-c"} {
		if !strings.Contains(js, want) {
			t.Errorf("interactive args missing %q in: %s", want, js)
		}
	}
	if strings.Contains(js, "--append-system-prompt") {
		t.Errorf("no --append-system-prompt should be added when $SF_CLAUDE_PROMPT_FILE is unset: %s", js)
	}

	ta := TaskArgs(target, Options{Task: "do x", Effort: "high", JSON: true})
	if ta[0] != "-p" || ta[1] != "do x" {
		t.Errorf("task args should start with -p \"do x\", got %v", ta[:2])
	}
	tjs := strings.Join(ta, " ")
	for _, want := range []string{"--effort high", "--output-format json"} {
		if !strings.Contains(tjs, want) {
			t.Errorf("task args missing %q in: %s", want, tjs)
		}
	}
}

func TestBaseArgsPromptFile(t *testing.T) {
	target := Target{Name: "myproj", Dir: "/w/myproj"}
	prompt := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(prompt, []byte("extra rules go here"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SF_CLAUDE_PROMPT_FILE", prompt)

	ia := InteractiveArgs(target, Options{})
	js := strings.Join(ia, " ")
	if !strings.Contains(js, "--append-system-prompt extra rules go here") {
		t.Errorf("expected the prompt file's contents to be appended: %s", js)
	}
}

func TestBaseArgsPromptFileUnreadable(t *testing.T) {
	target := Target{Name: "myproj", Dir: "/w/myproj"}
	t.Setenv("SF_CLAUDE_PROMPT_FILE", filepath.Join(t.TempDir(), "nope.md"))

	ia := InteractiveArgs(target, Options{})
	js := strings.Join(ia, " ")
	if strings.Contains(js, "--append-system-prompt") {
		t.Errorf("an unreadable prompt file should be treated as absent: %s", js)
	}
}

func TestValidate(t *testing.T) {
	if err := validate("effort", "", validEfforts); err != nil {
		t.Errorf("empty should be valid (default): %v", err)
	}
	if err := validate("effort", "high", validEfforts); err != nil {
		t.Errorf("high should be valid: %v", err)
	}
	if err := validate("effort", "bogus", validEfforts); err == nil {
		t.Error("bogus effort should be invalid")
	}
	if err := validate("permission-mode", "auto", validPermissionModes); err != nil {
		t.Errorf("auto should be a valid permission mode: %v", err)
	}
}

// childEnv must stamp the session's project name/root so `sf <proj>` tools in
// the launched session target this launch (the fork dir for a worktree
// session) without the agent needing `cd` or `--root`.
func TestChildEnvStampsProjectRoot(t *testing.T) {
	target := Target{Name: "myproj", Dir: "/w/wt/myproj-s2"}
	env := childEnv(target)
	var tag, root bool
	for _, kv := range env {
		if kv == "SOFIA_TAG=myproj" {
			tag = true
		}
		if kv == "SOFIA_PROJECT_ROOT=/w/wt/myproj-s2" {
			root = true
		}
	}
	if !tag || !root {
		t.Errorf("childEnv missing bindings: SOFIA_TAG=%v SOFIA_PROJECT_ROOT=%v", tag, root)
	}
}

// stubClaude writes a fake claude that prints RESULT and exits with code.
func stubClaude(t *testing.T, code int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude-stub")
	body := "#!/bin/sh\nprintf 'RESULT\\n'\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunTaskStdout(t *testing.T) {
	var buf bytes.Buffer
	code, err := runTask(stubClaude(t, 0), nil, Target{Dir: t.TempDir()}, "", false, &buf)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), "RESULT") {
		t.Errorf("stdout = %q, want RESULT", buf.String())
	}
}

func TestRunTaskExitCode(t *testing.T) {
	var buf bytes.Buffer
	code, err := runTask(stubClaude(t, 3), nil, Target{Dir: t.TempDir()}, "", false, &buf)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
}

func TestRunTaskOutAndQuiet(t *testing.T) {
	out := filepath.Join(t.TempDir(), "r.md")

	// --out without --quiet: tee to stdout AND file.
	var buf bytes.Buffer
	if _, err := runTask(stubClaude(t, 0), nil, Target{Dir: t.TempDir()}, out, false, &buf); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "RESULT") {
		t.Errorf("file = %q, want RESULT", data)
	}
	if !strings.Contains(buf.String(), "RESULT") {
		t.Errorf("tee: stdout should also have RESULT, got %q", buf.String())
	}

	// --out --quiet: file only, stdout silent.
	var qbuf bytes.Buffer
	if _, err := runTask(stubClaude(t, 0), nil, Target{Dir: t.TempDir()}, out, true, &qbuf); err != nil {
		t.Fatal(err)
	}
	if qbuf.Len() != 0 {
		t.Errorf("quiet: stdout should be empty, got %q", qbuf.String())
	}
	data, _ = os.ReadFile(out)
	if !strings.Contains(string(data), "RESULT") {
		t.Errorf("quiet: file should still have RESULT, got %q", data)
	}
}

// TestRunDryRunInteractive exercises Run() end-to-end in dry-run mode — the
// one path that's safe to drive through Run itself without execing a real
// claude (interactive mode would otherwise replace the test process).
func TestRunDryRunInteractive(t *testing.T) {
	claudeDir := t.TempDir()
	stub := filepath.Join(claudeDir, "claude")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", claudeDir)

	target := Target{Name: "myproj", Dir: t.TempDir()}
	var buf bytes.Buffer
	code, err := Run(target, Options{Model: "opus", DryRun: true}, &buf)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	out := buf.String()
	for _, want := range []string{"cd " + target.Dir, "SOFIA_TAG=myproj", "SOFIA_PROJECT_ROOT=" + target.Dir, "--model opus"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q in:\n%s", want, out)
		}
	}
}

func TestRunClaudeNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty PATH — claude can't resolve
	target := Target{Name: "myproj", Dir: t.TempDir()}
	if _, err := Run(target, Options{DryRun: true}, &bytes.Buffer{}); err == nil {
		t.Error("expected an error when claude isn't on PATH")
	}
}

func TestForkSlug(t *testing.T) {
	cases := map[string]string{"2": "s2", "s2": "s2", "s10": "s10", "custom": "custom"}
	for in, want := range cases {
		if got := ForkSlug(in); got != want {
			t.Errorf("ForkSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveForkMissingScript(t *testing.T) {
	dir := t.TempDir()
	if _, err := ResolveFork(dir, "2", true); err == nil {
		t.Fatal("expected error when dev/worktree.sh is absent")
	} else if !strings.Contains(err.Error(), "worktree.sh") {
		t.Errorf("error should mention worktree.sh: %v", err)
	}
}

// fakeWorktreeScript writes a dev/worktree.sh that always resolves fork
// selector <slug> to <dir>/dev/fork-<slug>, printing that dir for `dir` and
// creating it for `up`/`new`.
func fakeWorktreeScript(t *testing.T, projectDir string) {
	t.Helper()
	devDir := filepath.Join(projectDir, "dev")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `#!/bin/sh
slug="$2"
dir="$(dirname "$0")/fork-$slug"
case "$1" in
  dir) echo "$dir" ;;
  up|new) mkdir -p "$dir" ;;
esac
`
	if err := os.WriteFile(filepath.Join(devDir, "worktree.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveForkDryRun(t *testing.T) {
	dir := t.TempDir()
	fakeWorktreeScript(t, dir)

	got, err := ResolveFork(dir, "2", true)
	if err != nil {
		t.Fatalf("ResolveFork: %v", err)
	}
	want := filepath.Join(dir, "dev", "fork-s2")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if dirExists(want) {
		t.Error("dry-run must not create the fork dir")
	}
}

func TestResolveForkCreates(t *testing.T) {
	dir := t.TempDir()
	fakeWorktreeScript(t, dir)

	got, err := ResolveFork(dir, "3", false)
	if err != nil {
		t.Fatalf("ResolveFork: %v", err)
	}
	if !dirExists(got) {
		t.Errorf("fork dir %q should have been created", got)
	}
}
