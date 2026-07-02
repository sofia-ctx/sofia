package cli

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/plugin"
)

func TestAttachPluginShims(t *testing.T) {
	root := &cobra.Command{Use: "sf"}
	ds := []plugin.Descriptor{
		{
			Name:    "hello",
			Kind:    plugin.Managed,
			Enabled: true,
			Manifest: plugin.Manifest{
				Description: "greeter",
				Commands:    []plugin.Command{{Path: "greet", Short: "say hi"}},
			},
		},
		{
			Name:    "off",
			Kind:    plugin.Managed,
			Enabled: false, // disabled → must not be grafted
			Reason:  "protocol too new",
		},
	}

	attachPluginShims(root, ds)

	if _, _, err := root.Find([]string{"hello", "greet"}); err != nil {
		t.Errorf("shim `sf hello greet` not attached: %v", err)
	}
	hello, _, err := root.Find([]string{"hello"})
	if err != nil || hello.GroupID != pluginGroupID {
		t.Errorf("plugin group command missing or ungrouped: cmd=%v err=%v", hello, err)
	}
	var hasGroup bool
	for _, g := range root.Groups() {
		if g.ID == pluginGroupID {
			hasGroup = true
		}
	}
	if !hasGroup {
		t.Error("Plugins: help group was not added")
	}
	for _, c := range root.Commands() {
		if c.Name() == "off" {
			t.Error("disabled plugin must not be grafted onto the tree")
		}
	}
}

func TestAttachPluginShims_NoneAddsNoGroup(t *testing.T) {
	root := &cobra.Command{Use: "sf"}
	attachPluginShims(root, nil)
	if len(root.Groups()) != 0 {
		t.Error("no plugins should mean no empty Plugins: group")
	}
}
