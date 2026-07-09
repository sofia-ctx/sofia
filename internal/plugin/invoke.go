package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/envfile"
)

// capStdinJSON is the capability a "rich" plugin advertises to receive a JSON
// request on stdin instead of relying on argv alone. Absent it, the host
// connects the caller's stdin straight through (so an interactive/streaming
// plugin still works) and passes everything via argv + SOFIA_* env.
const capStdinJSON = "stdin-json"

// envFile is the per-plugin config file the host resolves declared settings
// into, kept inside the managed plugin's own directory next to its manifest.
const envFile = ".env"

// InvokeRequest is one host→plugin call. Command is nil for a passthrough
// plugin (`sf <name> …`); otherwise it names the declared subcommand, whose
// path is prepended to the plugin's argv. Stdout/Stderr/Stdin are the streams
// the plugin inherits — Stdout is metered for telemetry.
type InvokeRequest struct {
	Descriptor Descriptor
	Command    *Command
	Args       []string
	Stdout     io.Writer
	Stderr     io.Writer
	Stdin      io.Reader
}

// Request is the JSON document a "stdin-json"-capable plugin receives on stdin.
// It carries the same context as the SOFIA_* env vars in a structured form, for
// plugins that prefer parsing one object over reading the environment.
type Request struct {
	Argv        []string          `json:"argv"`
	ProjectRoot string            `json:"project_root"`
	Format      string            `json:"format"`
	Tag         string            `json:"tag"`
	SessionID   string            `json:"session_id,omitempty"`
	Source      string            `json:"source"`
	Settings    map[string]string `json:"settings,omitempty"`
}

// Invoke runs a plugin for one resolved command and streams its output, writing
// exactly one call-log line for the invocation — whatever happens. The tracker
// is started before any fallible step and finished in a defer, so a missing
// executable, a settings-resolution failure, a clean run, or a crash each land
// as one line tagged `<plugin>.<command>` with the real exit code and metered
// output. The plugin author writes no telemetry code.
func Invoke(ctx context.Context, req InvokeRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d := req.Descriptor
	argv := commandArgv(req.Command, req.Args)

	tracker := calllog.Start(toolName(d, req.Command), append([]string(nil), req.Args...))
	counter := &calllog.Counter{W: req.Stdout}
	var runErr error
	defer func() {
		tracker.RecordOutput(counter)
		tracker.Finish(runErr)
	}()

	if d.Exec == "" {
		runErr = fmt.Errorf("plugin %q has no runnable executable", d.Name)
		return runErr
	}

	settings, err := resolveSettings(d)
	if err != nil {
		runErr = err
		return runErr
	}

	c := exec.CommandContext(ctx, d.Exec, argv...)
	c.Env = childEnv(d, settings)
	c.Stdout = counter
	c.Stderr = req.Stderr

	if d.Manifest.HasCapability(capStdinJSON) {
		payload, err := json.Marshal(buildRequest(d, argv, settings))
		if err != nil {
			runErr = fmt.Errorf("plugin %s: build stdin request: %w", d.Name, err)
			return runErr
		}
		c.Stdin = bytes.NewReader(payload)
	} else {
		c.Stdin = req.Stdin
	}

	err = c.Run()
	code := exitCode(err)
	tracker.SetExitCode(code)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			runErr = fmt.Errorf("plugin %s exited with code %d", d.Name, code)
		} else {
			runErr = fmt.Errorf("plugin %s failed to start: %w", d.Name, err)
		}
		return runErr
	}
	return nil
}

// commandArgv builds the argv handed to the plugin: the declared command path
// (split on spaces or slashes) followed by the user's args. A passthrough
// plugin (nil command) receives the user's args verbatim.
func commandArgv(cmd *Command, args []string) []string {
	if cmd == nil || cmd.Path == "" {
		return args
	}
	parts := splitPath(cmd.Path)
	out := make([]string, 0, len(parts)+len(args))
	out = append(out, parts...)
	out = append(out, args...)
	return out
}

// toolName is the dotted call-log identity for the invocation, matching
// calllog's own `<group>.<sub>` convention: `<plugin>.<command-path>`, or just
// `<plugin>` for a passthrough plugin.
func toolName(d Descriptor, cmd *Command) string {
	if cmd == nil || cmd.Path == "" {
		return d.Name
	}
	return d.Name + "." + strings.Join(splitPath(cmd.Path), ".")
}

// splitPath splits a manifest command path on spaces or slashes into segments.
func splitPath(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == ' ' || r == '/' })
}

// childEnv is the plugin's environment: the host's own environment plus the
// SOFIA_* contract, deduplicated so the injected values win (glibc getenv
// returns the first match, so a naive append would be shadowed by an inherited
// duplicate).
func childEnv(_ Descriptor, settings map[string]string) []string {
	vals := map[string]string{}
	var order []string
	put := func(k, v string) {
		if _, ok := vals[k]; !ok {
			order = append(order, k)
		}
		vals[k] = v
	}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			put(kv[:i], kv[i+1:])
		}
	}
	put("SOFIA_PROJECT_ROOT", firstNonEmpty(vals["SOFIA_PROJECT_ROOT"], projectRoot()))
	put("SOFIA_FORMAT", firstNonEmpty(vals["SOFIA_FORMAT"], "toon"))
	// SOFIA_PLUGIN marks the child as a plugin subprocess so a plugin built
	// from sofia's own libraries can suppress its own call-log line — the host
	// already logs the invocation (see Invoke), and without this a plugin that
	// wraps its command in calllog.Run would double-count every call.
	put("SOFIA_PLUGIN", "1")
	put("SOFIA_TAG", calllog.ProjectTag())
	put("SOFIA_SOURCE", calllog.Source())
	if sid := calllog.SessionID(); sid != "" {
		put("SOFIA_SESSION_ID", sid)
	}
	for k, v := range settings {
		put(k, v)
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+vals[k])
	}
	return out
}

func buildRequest(d Descriptor, argv []string, settings map[string]string) Request {
	return Request{
		Argv:        argv,
		ProjectRoot: firstNonEmpty(os.Getenv("SOFIA_PROJECT_ROOT"), projectRoot()),
		Format:      firstNonEmpty(os.Getenv("SOFIA_FORMAT"), "toon"),
		Tag:         calllog.ProjectTag(),
		SessionID:   calllog.SessionID(),
		Source:      calllog.Source(),
		Settings:    settings,
	}
}

// resolveSettings resolves the plugin's declared config through the same
// envfile machinery the host uses for its own project config, backed by a .env
// inside the managed plugin's directory. A plugin with no settings resolves to
// nil with no I/O; a required setting missing on a non-TTY surfaces as the
// invocation error (the plugin is misconfigured, and says so).
func resolveSettings(d Descriptor) (map[string]string, error) {
	if len(d.Manifest.Settings) == 0 || d.Dir == "" {
		return nil, nil
	}
	fields := make([]envfile.Field, 0, len(d.Manifest.Settings))
	for _, s := range d.Manifest.Settings {
		fields = append(fields, s.Field())
	}
	return envfile.Resolve(filepath.Join(d.Dir, envFile), fields)
}

// projectRoot returns the repo root of the cwd (nearest ancestor with a .git
// entry), falling back to the cwd. It parallels calllog.deriveProjectFromCwd
// but yields the full path, which is what SOFIA_PROJECT_ROOT carries.
func projectRoot() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return ""
	}
	for dir := cwd; ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

// exitCode extracts a process exit code from cmd.Run's error: 0 on success, the
// real code for a non-zero exit, and 127 when the binary couldn't be executed
// at all (the shell's "command not found" convention).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 127
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
