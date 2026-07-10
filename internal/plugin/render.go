package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sofia-ctx/sofia/pkg/toon"
)

// status is the enabled/disabled word shown for a descriptor.
func status(d Descriptor) string {
	if d.Enabled {
		return "enabled"
	}
	return "disabled"
}

// listRow is the flat per-plugin summary `sf plugin list` renders.
type listRow struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func rows(ds []Descriptor) []listRow {
	out := make([]listRow, 0, len(ds))
	for _, d := range ds {
		out = append(out, listRow{
			Name:    d.Name,
			Kind:    string(d.Kind),
			Status:  status(d),
			Version: d.Manifest.Version,
			Reason:  d.Reason,
		})
	}
	return out
}

// RenderList writes the plugin list in the requested format (toon|md|json),
// following the same --format convention as every other sf tool.
func RenderList(w io.Writer, format string, ds []Descriptor) error {
	rs := rows(ds)
	switch format {
	case "json":
		return writeJSON(w, map[string]any{"plugins": rs})
	case "md":
		fmt.Fprintln(w, "| Name | Kind | Status | Version | Reason |")
		fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
		for _, r := range rs {
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", r.Name, r.Kind, r.Status, r.Version, r.Reason)
		}
		return nil
	default:
		fmt.Fprintf(w, "plugins[%d]{name,kind,status,version,reason}:\n", len(rs))
		for _, r := range rs {
			fmt.Fprintf(w, "%s%s,%s,%s,%s,%s\n", toon.Indent,
				toon.Scalar(r.Name), r.Kind, r.Status, toon.Scalar(r.Version), toon.Scalar(r.Reason))
		}
		return nil
	}
}

// RenderInfo writes one plugin's full manifest and status.
func RenderInfo(w io.Writer, format string, d Descriptor) error {
	switch format {
	case "json":
		return writeJSON(w, d)
	case "md":
		return renderInfoMarkdown(w, d)
	default:
		return renderInfoTOON(w, d)
	}
}

func renderInfoTOON(w io.Writer, d Descriptor) error {
	m := d.Manifest
	fmt.Fprintln(w, "plugin:")
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(w, "%s%s: %s\n", toon.Indent, k, toon.Scalar(v))
		}
	}
	kv("name", d.Name)
	kv("kind", string(d.Kind))
	kv("status", status(d))
	kv("reason", d.Reason)
	kv("protocol", m.Protocol)
	kv("version", m.Version)
	kv("min_sf", m.MinSF)
	kv("exec", d.Exec)
	kv("description", m.Description)

	if len(m.Commands) > 0 {
		fmt.Fprintf(w, "commands[%d]{path,short}:\n", len(m.Commands))
		for _, c := range m.Commands {
			fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(c.Path), toon.Scalar(c.Short))
		}
	}
	if len(m.Settings) > 0 {
		fmt.Fprintf(w, "settings[%d]{key,required,default,description}:\n", len(m.Settings))
		for _, s := range m.Settings {
			fmt.Fprintf(w, "%s%s,%t,%s,%s\n", toon.Indent,
				toon.Scalar(s.Key), s.Required, toon.Scalar(s.Default), toon.Scalar(s.Description))
		}
	}
	if len(m.Capabilities) > 0 {
		fmt.Fprintf(w, "capabilities: %s\n", toon.JoinList(m.Capabilities))
	}
	return nil
}

func renderInfoMarkdown(w io.Writer, d Descriptor) error {
	m := d.Manifest
	fmt.Fprintf(w, "# %s\n\n", d.Name)
	fmt.Fprintf(w, "- kind: %s\n", d.Kind)
	fmt.Fprintf(w, "- status: %s\n", status(d))
	if d.Reason != "" {
		fmt.Fprintf(w, "- reason: %s\n", d.Reason)
	}
	fmt.Fprintf(w, "- protocol: %s\n", m.Protocol)
	if m.Version != "" {
		fmt.Fprintf(w, "- version: %s\n", m.Version)
	}
	if m.MinSF != "" {
		fmt.Fprintf(w, "- min_sf: %s\n", m.MinSF)
	}
	fmt.Fprintf(w, "- exec: %s\n", d.Exec)
	if m.Description != "" {
		fmt.Fprintf(w, "- description: %s\n", m.Description)
	}
	if len(m.Capabilities) > 0 {
		fmt.Fprintf(w, "- capabilities: %s\n", strings.Join(m.Capabilities, ", "))
	}
	if len(m.Commands) > 0 {
		fmt.Fprint(w, "\n## Commands\n\n")
		fmt.Fprintln(w, "| Path | Short |")
		fmt.Fprintln(w, "| --- | --- |")
		for _, c := range m.Commands {
			fmt.Fprintf(w, "| %s | %s |\n", c.Path, c.Short)
		}
	}
	if len(m.Settings) > 0 {
		fmt.Fprint(w, "\n## Settings\n\n")
		fmt.Fprintln(w, "| Key | Required | Default | Description |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
		for _, s := range m.Settings {
			fmt.Fprintf(w, "| %s | %t | %s | %s |\n", s.Key, s.Required, s.Default, s.Description)
		}
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
