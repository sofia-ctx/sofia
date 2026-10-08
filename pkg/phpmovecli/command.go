// Package phpmovecli shares the move CLI between sf and project plugins.
package phpmovecli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sofia-ctx/sofia/pkg/phpmove"
	"github.com/spf13/cobra"
)

const maxOutputBytes = 4 << 20

// NewCommand accepts a project root resolver, or nil for the standard Composer
// root discovery. It does no I/O until a subcommand actually runs.
func NewCommand(resolve func(string) (string, error)) *cobra.Command {
	if resolve == nil {
		resolve = ResolveRoot
	}
	var rootFlag, out string
	var skip []string
	var diff, acceptWarnings bool
	cmd := &cobra.Command{Use: "move", Short: "Plan and apply reviewed PHP file or directory moves", Long: `Move an exact project-relative file or directory to a destination that does not exist.
PHP declarations and resolved references follow Composer PSR-4 mappings, including
autoload-dev. Original source formatting is preserved. Files with parse errors,
ambiguous mappings or unsupported declarations stop planning.

plan reads the project and writes a private JSON plan outside it (or in .sf-move).
apply revalidates that plan, journals original bytes and moves the files. Review
warnings before acknowledging them. Keep other writers out of the workspace.
No PHP, Git, network, deployment or Composer commands are executed.`, SilenceUsage: true}
	cmd.PersistentFlags().StringVar(&rootFlag, "root", "", "project root (else $SOFIA_PROJECT_ROOT or nearest composer.json)")
	plan := &cobra.Command{Use: "plan FROM TO --out PLAN", Short: "Inspect references and save a reviewable plan without changing sources", Args: cobra.ExactArgs(2)}
	plan.Flags().StringVar(&out, "out", "", "new JSON plan file outside the project or under .sf-move")
	plan.Flags().BoolVar(&diff, "diff", false, "include a unified source diff in the review output")
	plan.Flags().StringArrayVar(&skip, "exclude", nil, "explicitly omit a relative file or directory from reference search (repeatable)")
	_ = plan.MarkFlagRequired("out")
	plan.RunE = func(cmd *cobra.Command, args []string) error {
		root, err := resolve(rootFlag)
		if err != nil {
			return err
		}
		p, err := phpmove.BuildExcluding(root, args[0], args[1], skip)
		if err != nil {
			return err
		}
		data, err := phpmove.Encode(p)
		if err != nil {
			return err
		}
		output := Review(p, diff)
		output += fmt.Sprintf("Plan: %s\n", out)
		if len(output) > maxOutputBytes {
			return fmt.Errorf("review needs %d bytes (limit %d); plan was not saved; omit --diff and retry, then inspect the plan source edits", len(output), maxOutputBytes)
		}
		if err := savePlan(out, p.Root, data); err != nil {
			return err
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), output)
		return err
	}
	apply := &cobra.Command{Use: "apply PLAN", Short: "Revalidate a saved plan, back up sources and apply the move", Args: cobra.ExactArgs(1)}
	apply.Flags().BoolVar(&acceptWarnings, "accept-warnings", false, "acknowledge the manual review warnings in this exact plan")
	apply.RunE = func(cmd *cobra.Command, args []string) error {
		root, err := resolve(rootFlag)
		if err != nil {
			return err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return err
		}
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		p, err := phpmove.Decode(f)
		if err != nil {
			return err
		}
		if p.Root != root {
			return fmt.Errorf("plan belongs to %s, active project is %s", p.Root, root)
		}
		receipt, err := phpmove.Apply(p, acceptWarnings)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(receipt)
	}
	cmd.AddCommand(plan, apply)
	return cmd
}

// ResolveRoot follows explicit flag, session binding, then parent discovery.
func ResolveRoot(flag string) (string, error) {
	root := flag
	if root == "" {
		root = os.Getenv("SOFIA_PROJECT_ROOT")
	}
	if root != "" {
		root, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(filepath.Join(root, "composer.json")); err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("no composer.json in %s", root)
		}
		return root, nil
	}
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(root, "composer.json")); err == nil && info.Mode().IsRegular() {
			return root, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	return "", fmt.Errorf("no Composer project: pass --root or run inside a project")
}

func savePlan(name, root string, data []byte) error {
	abs, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return err
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < 2 || parts[0] != ".sf-move" {
			return fmt.Errorf("plan must be outside the project or under .sf-move so it cannot invalidate its own input snapshot")
		}
		if parts[1] == "lock" || strings.HasPrefix(parts[1], "txn-") {
			return fmt.Errorf("plan path is reserved for move transaction state")
		}
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(abs)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

// Review is deterministic and includes every file, exclusion and warning.
// The diff uses one hunk per file, without pretending to be a Git rename patch.
func Review(p *phpmove.Plan, diff bool) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Move: %s -> %s\nRoot: %s\nFiles: %d, classes: %d, review warnings: %d\n", p.From, p.To, p.Root, len(p.Changes), len(p.Classes), len(p.Warnings))
	var names []string
	for name := range p.Classes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&out, "  class %s -> %s\n", name, p.Classes[name])
	}
	for _, c := range p.Changes {
		if c.From != c.To {
			fmt.Fprintf(&out, "  move %s -> %s (%d edits)\n", c.From, c.To, len(c.Edits))
		} else {
			fmt.Fprintf(&out, "  edit %s (%d edits)\n", c.From, len(c.Edits))
		}
	}
	for _, d := range p.Directories {
		fmt.Fprintf(&out, "  directory %s -> %s\n", d.From, d.To)
	}
	out.WriteString("Scan excludes components: .git, .sf-move, .idea, vendor, node_modules, var\n")
	for _, omitted := range p.Skip {
		fmt.Fprintf(&out, "  explicit exclusion %s\n", omitted)
	}
	out.WriteString("Reference coverage: PHP class syntax; selected text extensions are searched for review hints. Dynamic references and other file formats require review.\n")
	for _, excluded := range p.Excluded {
		fmt.Fprintf(&out, "  excluded %s\n", excluded)
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(&out, "  warning %s:%d [%s] %s\n", w.Path, w.Line, w.Kind, w.Detail)
	}
	if diff {
		for _, c := range p.Changes {
			sourceDiff(&out, c)
		}
	}
	return out.String()
}
func sourceDiff(out *strings.Builder, c phpmove.Change) {
	if bytes.Equal(c.Before, c.After) {
		return
	}
	fmt.Fprintf(out, "--- %s\n+++ %s\n", c.From, c.To)
	if !utf8.Valid(c.Before) || !utf8.Valid(c.After) || bytes.IndexByte(c.Before, 0) >= 0 || bytes.IndexByte(c.After, 0) >= 0 {
		out.WriteString("Binary contents differ\n")
		return
	}
	before, after := lines(c.Before), lines(c.After)
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	endBefore, endAfter := len(before), len(after)
	for endBefore > start && endAfter > start && before[endBefore-1] == after[endAfter-1] {
		endBefore--
		endAfter--
	}
	// Three context lines on either side, including unchanged middle lines.
	start = max(0, start-3)
	endBefore = min(len(before), endBefore+3)
	endAfter = min(len(after), endAfter+3)
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", start+1, endBefore-start, start+1, endAfter-start)
	for _, line := range before[start:endBefore] {
		diffLine(out, "-", line)
	}
	for _, line := range after[start:endAfter] {
		diffLine(out, "+", line)
	}
}
func lines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	result := strings.SplitAfter(string(data), "\n")
	if result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return result
}
func diffLine(out *strings.Builder, prefix, line string) {
	out.WriteString(prefix)
	out.WriteString(line)
	if !strings.HasSuffix(line, "\n") {
		out.WriteString("\n\\ No newline at end of file\n")
	}
}
