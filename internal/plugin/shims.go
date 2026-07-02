package plugin

import (
	"os"

	"github.com/spf13/cobra"
)

// BuildCommands turns enabled plugins into cobra commands ready to attach to the
// root `sf` tree. Disabled plugins are deliberately omitted from the tree — they
// remain visible only through `sf plugin list`/`info`, so `sf <disabled>` is a
// plain unknown-command rather than a half-working shim.
//
// A plugin with declared subcommands becomes a help-only group (`sf <name>`)
// with one leaf per command; a plugin with none becomes a single passthrough
// command. Leaves set DisableFlagParsing so flags like `--json` pass through to
// the plugin verbatim (git-subcommand behaviour) rather than being intercepted
// by cobra. Building the tree never executes a plugin — every leaf only execs on
// RunE, i.e. when the user actually runs it.
func BuildCommands(ds []Descriptor) []*cobra.Command {
	var out []*cobra.Command
	for _, d := range ds {
		if !d.Enabled {
			continue
		}
		out = append(out, buildOne(d))
	}
	return out
}

func buildOne(d Descriptor) *cobra.Command {
	if !d.IsGroup() {
		return passthroughCmd(d)
	}
	root := &cobra.Command{
		Use:          d.Name,
		Short:        shortFor(d),
		SilenceUsage: true,
	}
	for _, c := range d.Manifest.Commands {
		attachLeaf(root, d, c)
	}
	return root
}

// passthroughCmd is `sf <name> …` execing the plugin with the user's args.
func passthroughCmd(d Descriptor) *cobra.Command {
	dd := d
	return &cobra.Command{
		Use:                dd.Name,
		Short:              shortFor(dd),
		DisableFlagParsing: true,
		SilenceUsage:       true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Invoke(cmd.Context(), InvokeRequest{
				Descriptor: dd, Args: args,
				Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin,
			})
		},
	}
}

// attachLeaf wires a declared command into the group tree, creating intermediate
// group commands for a multi-segment path (e.g. "cache clear") as needed.
func attachLeaf(root *cobra.Command, d Descriptor, c Command) {
	segs := splitPath(c.Path)
	if len(segs) == 0 {
		return
	}
	parent := root
	for i, seg := range segs {
		last := i == len(segs)-1
		child := childNamed(parent, seg)
		if child == nil {
			child = &cobra.Command{Use: seg, SilenceUsage: true}
			parent.AddCommand(child)
		}
		if last {
			dd, cc := d, c
			child.Short = c.Short
			child.DisableFlagParsing = true
			child.Args = cobra.ArbitraryArgs
			child.RunE = func(cmd *cobra.Command, args []string) error {
				return Invoke(cmd.Context(), InvokeRequest{
					Descriptor: dd, Command: &cc, Args: args,
					Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin,
				})
			}
		}
		parent = child
	}
}

func childNamed(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func shortFor(d Descriptor) string {
	if d.Manifest.Description != "" {
		return d.Manifest.Description
	}
	return "plugin: " + d.Name
}
