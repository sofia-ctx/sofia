package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sofia-ctx/sofia/internal/plugin"
)

// originFile is the provenance marker plugin.InstallFromGit writes next to a
// git-installed plugin (see internal/plugin's manage.go). An exported pack
// references a plugin by path, not git, so a marker copied along with it
// would be stale — Export strips it from every plugin it captures.
const originFile = ".sf-origin.json"

// ExportOptions configures Export.
type ExportOptions struct {
	Name    string // the new pack's name
	Project string // project to capture; "" defaults to cwd
	Out     string // parent directory for the new pack; "" defaults to cwd
	Force   bool   // remove and recreate an existing <out>/<name>
}

// ExportResult reports what Export captured, for the CLI to summarize.
type ExportResult struct {
	Dir       string   // the new pack's directory
	HasAgents bool     // whether <project>/AGENTS.md was captured
	Plugins   []string // captured managed plugin names, sorted
}

// Export captures opts.Project's current sf footprint — its AGENTS.md, if
// any, and every installed managed plugin — into a new pack at
// filepath.Join(opts.Out, opts.Name). Unlike Install, it never reads an
// existing pack.yaml: it builds one from what it finds on the machine.
//
// Plugins are a machine-global concept, not a project one: plugin.Load()
// can't tell which of them this project actually uses, so every managed
// plugin is captured and the author is expected to trim pack.yaml down to
// what belongs. A PATH-convention plugin (a bare `sf-<name>` executable) has
// no copyable source and is never captured.
func Export(opts ExportOptions) (ExportResult, error) {
	if !nameRe.MatchString(opts.Name) {
		return ExportResult{}, fmt.Errorf("pack.yaml: invalid name %q (must match %s)", opts.Name, nameRe.String())
	}
	project, err := resolveProject(opts.Project)
	if err != nil {
		return ExportResult{}, err
	}
	outDir := opts.Out
	if outDir == "" {
		outDir = "."
	}
	dst := filepath.Join(outDir, opts.Name)

	agentsSrc := filepath.Join(project, "AGENTS.md")
	agentsPerm := os.FileMode(0o644)
	hasAgents := true
	if info, err := os.Stat(agentsSrc); err != nil {
		if !os.IsNotExist(err) {
			return ExportResult{}, err
		}
		hasAgents = false
	} else {
		agentsPerm = info.Mode().Perm()
	}

	managed := managedPlugins(plugin.Load())

	if !hasAgents && len(managed) == 0 {
		return ExportResult{}, fmt.Errorf("nothing to capture in %s: no AGENTS.md and no managed plugins installed (try `sf pack new %s` for an empty skeleton instead)", project, opts.Name)
	}

	if _, err := os.Stat(dst); err == nil {
		if !opts.Force {
			return ExportResult{}, fmt.Errorf("%s already exists (rerun with --force to overwrite)", dst)
		}
		if err := os.RemoveAll(dst); err != nil {
			return ExportResult{}, err
		}
	} else if !os.IsNotExist(err) {
		return ExportResult{}, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return ExportResult{}, err
	}

	if hasAgents {
		f := plannedFile{Src: agentsSrc, Perm: agentsPerm}
		if err := writeFile(f, filepath.Join(dst, "instructions", "AGENTS.md")); err != nil {
			return ExportResult{}, err
		}
	}

	names := make([]string, 0, len(managed))
	for _, d := range managed {
		pdst := filepath.Join(dst, "plugins", d.Name)
		if err := copyTree(d.Dir, pdst); err != nil {
			return ExportResult{}, err
		}
		if err := os.Remove(filepath.Join(pdst, originFile)); err != nil && !os.IsNotExist(err) {
			return ExportResult{}, err
		}
		names = append(names, d.Name)
	}

	manifest := renderExportManifest(opts.Name, hasAgents, names)
	if err := os.WriteFile(filepath.Join(dst, manifestFile), []byte(manifest), 0o644); err != nil {
		return ExportResult{}, err
	}

	return ExportResult{Dir: dst, HasAgents: hasAgents, Plugins: names}, nil
}

// managedPlugins filters ds down to managed plugins with a copyable source
// directory — a PATH-convention plugin has none.
func managedPlugins(ds []plugin.Descriptor) []plugin.Descriptor {
	var out []plugin.Descriptor
	for _, d := range ds {
		if d.Kind == plugin.Managed && d.Dir != "" {
			out = append(out, d)
		}
	}
	return out
}

// renderExportManifest builds the pack.yaml Export writes: a static
// description (no timestamps — exports must be deterministic) plus exactly
// the sections it actually captured.
func renderExportManifest(name string, hasAgents bool, plugins []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema: %d\nname: %s\ndescription: \"TODO: describe %s\"\n", ManifestSchema, name, name)
	if len(plugins) > 0 {
		b.WriteString("plugins:\n")
		for _, p := range plugins {
			fmt.Fprintf(&b, "  - path: plugins/%s\n", p)
		}
	}
	if hasAgents {
		b.WriteString("instructions:\n  - src: instructions/AGENTS.md\n    dest: AGENTS.md\n")
	}
	return b.String()
}
