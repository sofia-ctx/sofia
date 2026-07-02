package cli

import (
	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/internal/plugin"
)

// pluginGroupID is the `sf --help` section discovered plugins are listed under,
// alongside the built-in context/php/projects/infra groups.
const pluginGroupID = "plugins"

// AttachPlugins grafts discovered, enabled plugins onto RootCmd as shim
// commands and tells calllog which plugin groups to keep out of its central
// fallback. It is called once from main() before Execute — deliberately not in
// init(), so importing this package for tests never touches the developer's
// real ~/.local/share/sofia cache.
//
// Discovery reads the cached metadata index (plugin.Load), so this costs one
// file read, not one fork per plugin — `sf --help` never execs a plugin just to
// build the tree.
func AttachPlugins() {
	ds := plugin.Load()
	attachPluginShims(RootCmd, ds)
	calllog.RegisterPluginGroups(plugin.GroupNames(ds))
}

// attachPluginShims adds the shim commands for ds to root, under a "Plugins:"
// help group (added only when there's at least one, so the section never shows
// up empty). Split out from AttachPlugins so tests can drive it against a bare
// root with hand-built descriptors.
func attachPluginShims(root *cobra.Command, ds []plugin.Descriptor) {
	cmds := plugin.BuildCommands(ds)
	if len(cmds) == 0 {
		return
	}
	root.AddGroup(&cobra.Group{ID: pluginGroupID, Title: "Plugins:"})
	for _, c := range cmds {
		c.GroupID = pluginGroupID
		root.AddCommand(c)
	}
}
