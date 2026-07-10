package adapter

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sofia-ctx/sofia/internal/common/grep"
	"github.com/sofia-ctx/sofia/internal/common/refs"
	"github.com/sofia-ctx/sofia/pkg/toon"
)

// grepGroup is one layer's slice of grep hits; refGroup the same for refs. Both
// reuse the exported grep.Hit / refs.Ref shapes so the grouped output speaks the
// same field vocabulary as `sf grep` / `sf refs`.
type grepGroup struct {
	Layer string     `json:"layer"`
	Hits  []grep.Hit `json:"hits"`
}

type refGroup struct {
	Layer string     `json:"layer"`
	Refs  []refs.Ref `json:"refs"`
}

var grepHitFields = []string{"file", "line", "col", "enclosing", "text"}
var refFields = []string{"kind", "file", "line", "enclosing", "text"}

// groupHits buckets every grep hit (across all patterns) by its layer, then
// returns the buckets in declared-layer order with "(unclassified)" last —
// skipping empty layers, so the output is only the layers actually hit.
func groupHits(cfg Config, r *grep.Result) []grepGroup {
	byLayer := map[string][]grep.Hit{}
	for _, pr := range r.Patterns {
		for _, h := range pr.Hits {
			byLayer[layerOf(cfg, h.File)] = append(byLayer[layerOf(cfg, h.File)], h)
		}
	}
	out := make([]grepGroup, 0, len(byLayer))
	for _, name := range orderedLayerNames(cfg, byLayer) {
		out = append(out, grepGroup{Layer: name, Hits: byLayer[name]})
	}
	return out
}

// groupRefs buckets refs by layer, same ordering discipline as groupHits.
func groupRefs(cfg Config, r *refs.Result) []refGroup {
	byLayer := map[string][]refs.Ref{}
	for _, ref := range r.Refs {
		byLayer[layerOf(cfg, ref.File)] = append(byLayer[layerOf(cfg, ref.File)], ref)
	}
	out := make([]refGroup, 0, len(byLayer))
	for _, name := range orderedLayerNames(cfg, byLayer) {
		out = append(out, refGroup{Layer: name, Refs: byLayer[name]})
	}
	return out
}

// layerOf classifies a file, mapping the empty (no-layer) result to the
// "(unclassified)" bucket name.
func layerOf(cfg Config, file string) string {
	if l := Classify(cfg, file); l != "" {
		return l
	}
	return unclassifiedLayer
}

// orderedLayerNames is the shared ordering rule: each declared layer that has
// entries in byLayer, in declared order, then "(unclassified)" last. The type
// parameter lets both the grep and refs bucket maps share one rule.
func orderedLayerNames[T any](cfg Config, byLayer map[string][]T) []string {
	var out []string
	for _, l := range cfg.Layers {
		if len(byLayer[l.Name]) > 0 {
			out = append(out, l.Name)
		}
	}
	if len(byLayer[unclassifiedLayer]) > 0 {
		out = append(out, unclassifiedLayer)
	}
	return out
}

func renderLayerList(w io.Writer, format string, cfg Config) error {
	switch format {
	case "json":
		return jsonEncode(w, cfg.Layers)
	case "md":
		fmt.Fprintf(w, "# layers (%d)\n\n", len(cfg.Layers))
		for _, l := range cfg.Layers {
			fmt.Fprintf(w, "- **%s** — %s\n", l.Name, strings.Join(l.Match, ", "))
		}
		return nil
	default:
		fmt.Fprintf(w, "layers[%d]{name,match}:\n", len(cfg.Layers))
		for _, l := range cfg.Layers {
			fmt.Fprintf(w, "%s%s,%s\n", toon.Indent, toon.Scalar(l.Name), toon.Scalar(strings.Join(l.Match, " ")))
		}
		return nil
	}
}

func renderClassify(w io.Writer, format, path, layer string) error {
	switch format {
	case "json":
		return jsonEncode(w, map[string]string{"path": path, "layer": layer})
	case "md":
		fmt.Fprintf(w, "%s → **%s**\n", path, layer)
		return nil
	default:
		fmt.Fprintf(w, "path: %s\nlayer: %s\n", toon.Scalar(path), toon.Scalar(layer))
		return nil
	}
}

func renderGrepGroups(w io.Writer, format string, groups []grepGroup) error {
	switch format {
	case "json":
		return jsonEncode(w, map[string]any{"groups": groups})
	case "md":
		if len(groups) == 0 {
			fmt.Fprintln(w, "no matches")
			return nil
		}
		for i, g := range groups {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "# %s    hits=%d\n\n", g.Layer, len(g.Hits))
			for _, h := range g.Hits {
				fmt.Fprintf(w, "  %s:%d", h.File, h.Line)
				if h.Enclosing != "" {
					fmt.Fprintf(w, "  %s", h.Enclosing)
				}
				fmt.Fprintf(w, "\n    %s\n", trim(h.Text, 200))
			}
		}
		return nil
	default:
		if len(groups) == 0 {
			fmt.Fprintln(w, "# no matches")
			return nil
		}
		for _, g := range groups {
			fmt.Fprintf(w, "%s{hits=%d}:\n", toon.Scalar(g.Layer), len(g.Hits))
			fmt.Fprintf(w, "%shits[%d]{%s}:\n", toon.Indent, len(g.Hits), strings.Join(grepHitFields, ","))
			rowIndent := toon.Indent + toon.Indent
			for _, h := range g.Hits {
				fmt.Fprintf(w, "%s%s,%d,%d,%s,%s\n",
					rowIndent, toon.Scalar(h.File), h.Line, h.Col,
					toon.Scalar(h.Enclosing), toon.Scalar(trim(h.Text, 200)))
			}
		}
		return nil
	}
}

func renderRefGroups(w io.Writer, format, symbol string, groups []refGroup) error {
	switch format {
	case "json":
		return jsonEncode(w, map[string]any{"symbol": symbol, "groups": groups})
	case "md":
		fmt.Fprintf(w, "# refs: %s\n", symbol)
		if len(groups) == 0 {
			fmt.Fprintln(w, "\nno references")
			return nil
		}
		for _, g := range groups {
			fmt.Fprintf(w, "\n## %s (%d)\n\n", g.Layer, len(g.Refs))
			for _, ref := range g.Refs {
				fmt.Fprintf(w, "- %s %s:%d", ref.Kind, ref.File, ref.Line)
				if ref.Enclosing != "" {
					fmt.Fprintf(w, "  %s", ref.Enclosing)
				}
				fmt.Fprintln(w)
			}
		}
		return nil
	default:
		fmt.Fprintf(w, "# refs: %s\n", toon.Scalar(symbol))
		if len(groups) == 0 {
			fmt.Fprintln(w, "# no references")
			return nil
		}
		for _, g := range groups {
			fmt.Fprintf(w, "%s{refs=%d}:\n", toon.Scalar(g.Layer), len(g.Refs))
			fmt.Fprintf(w, "%srefs[%d]{%s}:\n", toon.Indent, len(g.Refs), strings.Join(refFields, ","))
			rowIndent := toon.Indent + toon.Indent
			for _, ref := range g.Refs {
				fmt.Fprintf(w, "%s%s,%s,%d,%s,%s\n",
					rowIndent, toon.Scalar(ref.Kind), toon.Scalar(ref.File), ref.Line,
					toon.Scalar(ref.Enclosing), toon.Scalar(trim(ref.Text, 200)))
			}
		}
		return nil
	}
}

func jsonEncode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func trim(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
