package launch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Overlays are personal, per-project instructions and commands kept in a
// private git repo, separate from the project's own shared AGENTS.md. On
// `sf claude <tag>` the overlay for that tag contributes up to three things:
// its AGENTS.md is injected via --append-system-prompt (so it outranks the
// repo's AGENTS.md, which claude reads as project context); if the dir is a
// Claude Code plugin (a .claude-plugin/plugin.json with commands/) it's loaded
// via --plugin-dir, so its slash-commands are available for the session; and
// the dir is added with --add-dir so the session can read and edit the overlay
// and push it back. Layout is by convention: <overlaysRoot>/<repo>/<tag>/ — one
// or more cloned repos, each holding a dir per project tag.

// overlayFile is a tag dir's personal instructions, injected as an
// authoritative system prompt.
const overlayFile = "AGENTS.md"

// overlayPluginManifest marks a tag dir as a Claude Code plugin: sf loads it
// with --plugin-dir so the overlay's commands/ (and any skills/agents) become
// available for the session, namespaced under the plugin's own name.
const overlayPluginManifest = ".claude-plugin/plugin.json"

// overlayPreamble frames the overlay text as authoritative over the repo's own
// AGENTS.md and points the agent at the editable source dir. %s is that dir.
const overlayPreamble = `# Personal project overlay (authoritative)
These are your personal instructions for this project, loaded from your private
overlay repo at %s. On any conflict they take precedence over the repository's
own AGENTS.md/CLAUDE.md. You may edit the overlay files there and commit them
back to the overlay repo.

`

// overlaysRoot is where cloned overlay repos live: $SF_CLAUDE_OVERLAY_DIR
// override (used by tests), else $XDG_DATA_HOME/sofia/overlays, else
// ~/.local/share/sofia/overlays — data-home, matching where plugins and packs
// keep their clones. Empty string if no home can be resolved.
func overlaysRoot() string {
	if p := os.Getenv("SF_CLAUDE_OVERLAY_DIR"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "sofia", "overlays")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "sofia", "overlays")
	}
	return ""
}

// overlayMatch is a resolved overlay for a tag: the dir plus what it carries —
// an AGENTS.md path (injected as an authoritative system prompt, "" if absent)
// and whether the dir is a Claude Code plugin (loaded with --plugin-dir for its
// commands).
type overlayMatch struct {
	dir    string
	agents string
	plugin bool
}

// isOverlayDir reports whether a tag dir carries anything sf injects — personal
// instructions (AGENTS.md) or a plugin manifest (commands). An empty dir, or a
// stray dir with neither, is not an overlay.
func isOverlayDir(dir string) bool {
	return fileExists(filepath.Join(dir, overlayFile)) ||
		fileExists(filepath.Join(dir, overlayPluginManifest))
}

// resolveOverlay finds the overlay for a project tag by scanning each cloned
// repo under overlaysRoot() for a <repo>/<tag>/ that isOverlayDir. os.ReadDir
// returns entries sorted, so a match is deterministic; when several repos define
// the same tag it warns and takes the first. ok is false when nothing matches.
func resolveOverlay(name string) (overlayMatch, bool) {
	if name == "" {
		return overlayMatch{}, false
	}
	root := overlaysRoot()
	if root == "" {
		return overlayMatch{}, false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return overlayMatch{}, false
	}

	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(root, e.Name(), name)
		if isOverlayDir(cand) {
			matches = append(matches, cand)
		}
	}
	if len(matches) == 0 {
		return overlayMatch{}, false
	}
	if len(matches) > 1 {
		fmt.Fprintf(os.Stderr, "note: overlay %q is defined by %d repos; using %s\n", name, len(matches), matches[0])
	}
	dir := matches[0]
	m := overlayMatch{dir: dir, plugin: fileExists(filepath.Join(dir, overlayPluginManifest))}
	if agents := filepath.Join(dir, overlayFile); fileExists(agents) {
		m.agents = agents
	}
	return m, true
}

// overlayPrompt reads a tag's overlay file and wraps it in the precedence
// preamble. "" if the file can't be read (the caller then adds no prompt).
func overlayPrompt(dir, promptFile string) string {
	data, err := os.ReadFile(promptFile)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(overlayPreamble, dir) + string(data)
}

// overlayTags lists the project tags a cloned repo provides (subdirs that
// isOverlayDir), sorted.
func overlayTags(repo string) []string {
	entries, err := os.ReadDir(repo)
	if err != nil {
		return nil
	}
	var tags []string
	for _, e := range entries {
		if e.IsDir() && isOverlayDir(filepath.Join(repo, e.Name())) {
			tags = append(tags, e.Name())
		}
	}
	return tags
}

// overlayRepoName derives a clone dir name from a git URL:
// git@github.com:sofia-ctx/overlays.git -> overlays.
func overlayRepoName(url string) string {
	s := strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// overlayClone clones url into dest, streaming git's output so SSH prompts and
// progress are visible.
func overlayClone(url, dest string) error {
	cmd := exec.Command("git", "clone", url, dest)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s: %w", url, err)
	}
	return nil
}

// overlaySync fast-forwards the clone and pushes any local commits (`git push`
// is a no-op when there's nothing to send).
func overlaySync(dir string) error {
	for _, gitArgs := range [][]string{
		{"-C", dir, "pull", "--ff-only"},
		{"-C", dir, "push"},
	} {
		cmd := exec.Command("git", gitArgs...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(gitArgs[2:], " "), err)
		}
	}
	return nil
}

// newOverlayCommand is the `sf claude overlay` group: manage the private repos
// that supply personal per-project overlays.
func newOverlayCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "overlay",
		Short: "Manage personal per-project instruction overlays",
		Long: `overlay manages private repos of personal, per-project instructions that
` + "`sf claude <tag>`" + ` loads on top of a project — with priority over the
project's own AGENTS.md — without putting your personal notes in the shared repo.

Layout is by convention: <overlaysRoot>/<repo>/<tag>/AGENTS.md, where <tag> is
the project name you launch (e.g. ` + "`sf claude projectA`" + ` reads the
projectA/ dir). overlaysRoot is $SF_CLAUDE_OVERLAY_DIR, else
$XDG_DATA_HOME/sofia/overlays.`,
	}
	cmd.AddCommand(
		newOverlayAddCommand(),
		newOverlaySyncCommand(),
		newOverlayListCommand(),
		newOverlayPathCommand(),
	)
	return cmd
}

func newOverlayAddCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "add <git-url> [name]",
		Short: "Clone a private overlay repo into the overlays root",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := overlaysRoot()
			if root == "" {
				return fmt.Errorf("no overlays root: set $SF_CLAUDE_OVERLAY_DIR or $XDG_DATA_HOME")
			}
			url := args[0]
			name := overlayRepoName(url)
			if len(args) == 2 {
				name = args[1]
			}
			dest := filepath.Join(root, name)
			if dirExists(dest) {
				return fmt.Errorf("overlay %q already exists at %s; run `sf claude overlay sync %s` to update", name, dest, name)
			}
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			if err := overlayClone(url, dest); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cloned %s -> %s\n", url, dest)
			return nil
		},
	}
}

func newOverlaySyncCommand() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "sync [name]",
		Short: "Pull and push an overlay repo (--all for every clone)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := overlaysRoot()
			if root == "" {
				return fmt.Errorf("no overlays root: set $SF_CLAUDE_OVERLAY_DIR or $XDG_DATA_HOME")
			}
			var names []string
			switch {
			case all:
				entries, _ := os.ReadDir(root)
				for _, e := range entries {
					if e.IsDir() {
						names = append(names, e.Name())
					}
				}
			case len(args) == 1:
				names = []string{args[0]}
			default:
				return fmt.Errorf("give an overlay name or --all")
			}
			if len(names) == 0 {
				return fmt.Errorf("no overlays under %s", root)
			}
			for _, n := range names {
				dir := filepath.Join(root, n)
				if !dirExists(dir) {
					return fmt.Errorf("overlay %q not found at %s", n, dir)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "syncing %s\n", n)
				if err := overlaySync(dir); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "sync every cloned overlay")
	return cmd
}

func newOverlayListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cloned overlay repos and the project tags they provide",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := overlaysRoot()
			out := cmd.OutOrStdout()
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) == 0 {
				fmt.Fprintf(out, "no overlays under %s\n", root)
				return nil
			}
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				fmt.Fprintln(out, e.Name())
				for _, tag := range overlayTags(filepath.Join(root, e.Name())) {
					fmt.Fprintf(out, "  %s\n", tag)
				}
			}
			return nil
		},
	}
}

func newOverlayPathCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "path <tag>",
		Short: "Print the overlay dir resolved for a project tag",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ok := resolveOverlay(args[0])
			if !ok {
				return fmt.Errorf("no overlay for %q under %s", args[0], overlaysRoot())
			}
			fmt.Fprintln(cmd.OutOrStdout(), m.dir)
			return nil
		},
	}
}
