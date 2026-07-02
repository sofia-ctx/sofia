package plugin

import (
	"strings"
	"testing"
)

func TestParseManifest_Full(t *testing.T) {
	src := `
schema: 1
protocol: "1.0.0"
version: "0.3.1"
min_sf: "1.0.0"
description: "greet people"
exec: hello
commands:
  - path: greet
    short: "Print a greeting"
  - path: cache clear
    short: "Drop the cache"
capabilities:
  - stdin-json
settings:
  - key: HELLO_GREETING
    prompt: "Greeting word"
    description: "word used to greet"
    default: "Hello"
    required: true
adapter:
  kind: openapi
`
	m, err := ParseManifest([]byte(src))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Schema != 1 || m.Protocol != "1.0.0" || m.Version != "0.3.1" || m.MinSF != "1.0.0" {
		t.Errorf("core fields wrong: %+v", m)
	}
	if m.Exec != "hello" || m.Description != "greet people" {
		t.Errorf("exec/description wrong: %+v", m)
	}
	if len(m.Commands) != 2 || m.Commands[0].Path != "greet" || m.Commands[1].Path != "cache clear" {
		t.Errorf("commands wrong: %+v", m.Commands)
	}
	if !m.HasCapability("stdin-json") || m.HasCapability("nope") {
		t.Errorf("capabilities wrong: %+v", m.Capabilities)
	}
	if len(m.Settings) != 1 {
		t.Fatalf("settings wrong: %+v", m.Settings)
	}
	f := m.Settings[0].Field()
	if f.Key != "HELLO_GREETING" || f.Default != "Hello" || !f.Required || f.Prompt != "Greeting word" {
		t.Errorf("Setting.Field() wrong: %+v", f)
	}
	if m.Adapter == nil || m.Adapter.Kind != "openapi" {
		t.Errorf("adapter wrong: %+v", m.Adapter)
	}
}

// Forward-compat: an unrecognized top-level key must not fail the parse, the
// same principle LSP uses for capability negotiation.
func TestParseManifest_UnknownKeyIgnored(t *testing.T) {
	src := `
schema: 1
protocol: "1.0.0"
future_field:
  nested: true
  list: [a, b]
description: "still parses"
`
	m, err := ParseManifest([]byte(src))
	if err != nil {
		t.Fatalf("unknown key should be ignored, got error: %v", err)
	}
	if m.Description != "still parses" || m.Protocol != "1.0.0" {
		t.Errorf("known fields lost alongside unknown key: %+v", m)
	}
}

func TestParseManifest_InvalidYAML(t *testing.T) {
	if _, err := ParseManifest([]byte("commands: [unterminated")); err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestParseManifest_MinimalParsesButUndeclaredProtocol(t *testing.T) {
	// A valid-but-underspecified manifest parses; the missing protocol is
	// caught by compatibility gating, not by the parser.
	m, err := ParseManifest([]byte("description: bare\n"))
	if err != nil {
		t.Fatalf("minimal manifest should parse: %v", err)
	}
	if ok, reason := Compatible(m.Protocol, m.MinSF, HostProtocol); ok || !strings.Contains(reason, "protocol") {
		t.Errorf("expected disabled-with-reason for missing protocol, got ok=%v reason=%q", ok, reason)
	}
}
