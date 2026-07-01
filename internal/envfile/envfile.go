// Package envfile loads .env-style files, prompts interactively for missing
// required values, and writes them back. Designed for per-tool config that
// each binary keeps next to itself (e.g. bin/projects/<name>/.env).
package envfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/term"
)

// Field describes a single environment variable a tool needs.
type Field struct {
	Key         string
	Prompt      string                // shown to user when interactive
	Description string                // short help shown above the prompt
	Default     string                // used silently if no value found anywhere
	Required    bool                  // missing required → prompt (or error if non-TTY)
	Validator   func(string) error    // returns error if value is invalid
}

// Resolve fetches values for every Field in priority order:
//   1. existing entry in path
//   2. process environment ($KEY)
//   3. Default (silent fallback)
//   4. interactive prompt (Required only) — saved back to path
//
// All resolved values are returned. Stdin must be a TTY when prompting is
// needed; otherwise an error is returned listing the missing keys.
func Resolve(path string, fields []Field) (map[string]string, error) {
	existing, _ := Load(path)
	resolved := make(map[string]string, len(fields))
	var toPrompt []Field

	for _, f := range fields {
		v, ok := existing[f.Key]
		if !ok || v == "" {
			if env := os.Getenv(f.Key); env != "" {
				v, ok = env, true
			}
		}
		if (!ok || v == "") && f.Default != "" {
			v, ok = f.Default, true
		}
		if !ok || v == "" {
			if f.Required {
				toPrompt = append(toPrompt, f)
				continue
			}
			resolved[f.Key] = ""
			continue
		}
		if f.Validator != nil {
			if err := f.Validator(v); err != nil {
				return nil, fmt.Errorf("%s: %w", f.Key, err)
			}
		}
		resolved[f.Key] = v
	}

	if len(toPrompt) > 0 {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			var keys []string
			for _, f := range toPrompt {
				keys = append(keys, f.Key)
			}
			return nil, fmt.Errorf("missing required env vars %v and stdin is not a TTY; set them via %s or env", keys, path)
		}
		fmt.Fprintf(os.Stderr, "First-time setup: filling %s\n", path)
		reader := bufio.NewReader(os.Stdin)
		for _, f := range toPrompt {
			val, err := promptOne(reader, f)
			if err != nil {
				return nil, err
			}
			resolved[f.Key] = val
			existing[f.Key] = val
		}
		if err := Save(path, existing); err != nil {
			return nil, fmt.Errorf("save %s: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "Saved %s\n\n", path)
	}

	return resolved, nil
}

func promptOne(r *bufio.Reader, f Field) (string, error) {
	for {
		if f.Description != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", f.Description)
		}
		label := f.Prompt
		if label == "" {
			label = f.Key
		}
		if f.Default != "" {
			fmt.Fprintf(os.Stderr, "  %s [%s]: ", label, f.Default)
		} else {
			fmt.Fprintf(os.Stderr, "  %s: ", label)
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		v := strings.TrimSpace(line)
		if v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			fmt.Fprintln(os.Stderr, "  (required, please enter a value)")
			continue
		}
		if f.Validator != nil {
			if err := f.Validator(v); err != nil {
				fmt.Fprintf(os.Stderr, "  invalid: %v\n", err)
				continue
			}
		}
		return v, nil
	}
}

// Load parses a .env file. Missing files return an empty map and no error.
// Lines are KEY=VALUE; lines starting with `#` and blank lines are ignored.
// Surrounding double or single quotes are stripped from values.
func Load(path string) (map[string]string, error) {
	out := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			first, last := val[0], val[len(val)-1]
			if first == '"' && last == '"' {
				// double-quoted: reverse the escapes Save() applies.
				inner := val[1 : len(val)-1]
				inner = strings.ReplaceAll(inner, `\\`, "\x00") // protect literal \\
				inner = strings.ReplaceAll(inner, `\"`, `"`)
				inner = strings.ReplaceAll(inner, "\x00", `\`)
				val = inner
			} else if first == '\'' && last == '\'' {
				val = val[1 : len(val)-1]
			}
		}
		out[key] = val
	}
	return out, sc.Err()
}

// MigrateOnce copies legacyPath → currentPath on first run after the
// canonical location moves (e.g. when `.env` shifts under XDG). Best-effort:
// silently does nothing if currentPath already exists, legacyPath is missing,
// or any I/O step fails — the user will be prompted to refill the file
// through Resolve like a fresh install. On a successful copy a single-line
// notice is written to stderr so the move isn't invisible. The legacy file
// is left in place so downgrading to an older binary still finds its config.
func MigrateOnce(currentPath, legacyPath string) {
	if currentPath == "" || legacyPath == "" || currentPath == legacyPath {
		return
	}
	if _, err := os.Stat(currentPath); err == nil {
		return
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(currentPath), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(currentPath, data, 0o644); err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "Migrated .env: %s → %s\n", legacyPath, currentPath)
}

// Save writes the map to path in stable KEY=VALUE form, creating parent
// directories as needed. Values containing spaces or `#` are quoted.
func Save(path string, vals map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := vals[k]
		if strings.ContainsAny(v, " \t#\"'\\") {
			// Escape `\` first so we don't double-escape what's introduced for `"`.
			escaped := strings.ReplaceAll(v, `\`, `\\`)
			escaped = strings.ReplaceAll(escaped, `"`, `\"`)
			v = `"` + escaped + `"`
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
