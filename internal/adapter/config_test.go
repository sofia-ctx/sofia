package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

// specFor is a small helper to build the spec map a plugin's adapter block
// decodes into, keeping the table tests readable.
func specFor() map[string]any {
	return map[string]any{
		"root_key":     "APP_ROOT",
		"root_markers": []any{"composer.json"},
		"ext":          []any{"php"},
		"layers": []any{
			map[string]any{"name": "Domain", "match": []any{"src/Domain/**"}},
			map[string]any{"name": "Application", "match": []any{"src/Application/**"}},
		},
	}
}

func TestParseAndValidate_Valid(t *testing.T) {
	cfg, err := Parse("php-ddd", specFor())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Kind != "php-ddd" || cfg.RootKey != "APP_ROOT" {
		t.Errorf("kind/root_key wrong: %+v", cfg)
	}
	if len(cfg.RootMarkers) != 1 || cfg.RootMarkers[0] != "composer.json" {
		t.Errorf("root_markers wrong: %+v", cfg.RootMarkers)
	}
	// ext is normalized to a leading-dot, lower-case form.
	if len(cfg.Ext) != 1 || cfg.Ext[0] != ".php" {
		t.Errorf("ext not normalized: %+v", cfg.Ext)
	}
	if len(cfg.Layers) != 2 || cfg.Layers[0].Name != "Domain" {
		t.Errorf("layers wrong: %+v", cfg.Layers)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate on a good config: %v", err)
	}
}

func TestNormalizeExt_Variants(t *testing.T) {
	cfg, err := Parse("x", map[string]any{
		"root_markers": []any{"go.mod"},
		"ext":          []any{".PHP", "ts", "  ", ".vue"},
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := strings.Join(cfg.Ext, ",")
	if got != ".php,.ts,.vue" {
		t.Errorf("ext normalization = %q, want .php,.ts,.vue", got)
	}
}

func TestValidate_KindOnlyFails(t *testing.T) {
	// A block with only a kind (no root markers) parses but must not validate.
	cfg, err := Parse("bare", map[string]any{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "root_markers") {
		t.Errorf("want a root_markers error, got %v", err)
	}
}

func TestValidate_RequiresRootMarkers(t *testing.T) {
	cfg, _ := Parse("x", map[string]any{
		"layers": []any{map[string]any{"name": "A", "match": []any{"a/**"}}},
	})
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "root_markers") {
		t.Errorf("missing root_markers should fail, got %v", err)
	}
}

func TestValidate_UnsafeRootMarker(t *testing.T) {
	cfg, _ := Parse("x", map[string]any{"root_markers": []any{"../etc/passwd"}})
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "root_markers") {
		t.Errorf("unsafe root marker should fail, got %v", err)
	}
}

func TestValidate_DuplicateLayer(t *testing.T) {
	cfg, _ := Parse("x", map[string]any{
		"root_markers": []any{"go.mod"},
		"layers": []any{
			map[string]any{"name": "A", "match": []any{"a/**"}},
			map[string]any{"name": "A", "match": []any{"b/**"}},
		},
	})
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate layer name should fail, got %v", err)
	}
}

func TestValidate_EmptyLayerName(t *testing.T) {
	cfg, _ := Parse("x", map[string]any{
		"root_markers": []any{"go.mod"},
		"layers":       []any{map[string]any{"name": "  ", "match": []any{"a/**"}}},
	})
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("empty layer name should fail, got %v", err)
	}
}

func TestValidate_LayerMatchRequired(t *testing.T) {
	cfg, _ := Parse("x", map[string]any{
		"root_markers": []any{"go.mod"},
		"layers":       []any{map[string]any{"name": "A"}},
	})
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "match") {
		t.Errorf("layer without a match glob should fail, got %v", err)
	}
}

func TestValidate_UnsafeGlobRejected(t *testing.T) {
	cfg, _ := Parse("x", map[string]any{
		"root_markers": []any{"go.mod"},
		"layers":       []any{map[string]any{"name": "A", "match": []any{"../../*"}}},
	})
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "match") {
		t.Errorf("unsafe layer glob should fail, got %v", err)
	}
}

// TestFromJSONRoundtripMap guards the reason Parse re-marshals through YAML: the
// discovery cache (plugins.json) stores the adapter spec as JSON, so on a cache
// hit the spec map arrives with []interface{} lists and float64 numbers, not the
// shapes a fresh YAML parse produced. Parse must decode that shape identically.
func TestFromJSONRoundtripMap(t *testing.T) {
	spec := specFor()
	// Round-trip the whole spec through JSON, exactly as the plugins.json cache
	// does, so lists become []interface{} and any numbers become float64.
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripped map[string]any
	if err := json.Unmarshal(data, &roundtripped); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse("php-ddd", roundtripped)
	if err != nil {
		t.Fatalf("Parse after JSON round-trip: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate after JSON round-trip: %v", err)
	}
	if len(cfg.RootMarkers) != 1 || cfg.RootMarkers[0] != "composer.json" {
		t.Errorf("root_markers lost in round-trip: %+v", cfg.RootMarkers)
	}
	if len(cfg.Layers) != 2 || cfg.Layers[0].Name != "Domain" || cfg.Layers[1].Name != "Application" {
		t.Errorf("layers lost in round-trip: %+v", cfg.Layers)
	}
	if len(cfg.Ext) != 1 || cfg.Ext[0] != ".php" {
		t.Errorf("ext lost in round-trip: %+v", cfg.Ext)
	}
}
