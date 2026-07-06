package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

// expectedTools is the exact public-safe tool set the server must expose —
// no private project/launch/lab tools, and none of the mutating actions.
var expectedTools = []string{
	"code", "grep", "changed",
	"cc_ls", "cc_show", "cc_resume", "cc_prompts", "cc_bash", "cc_candidates",
	"composer_ls", "composer_show", "composer_check",
	"packagist_status",
	"github_ci", "github_pr", "github_branches",
	"vue_routes",
}

// connectInMemory wires a client to a freshly-built server over the SDK's
// in-memory transport pair and runs the initialize handshake (via Connect).
func connectInMemory(ctx context.Context, t *testing.T) *mcp.ClientSession {
	t.Helper()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := NewServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestListTools_ExactPublicSafeSet(t *testing.T) {
	ctx := context.Background()
	cs := connectInMemory(ctx, t)

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	got := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, name := range expectedTools {
		if !got[name] {
			t.Errorf("expected tool %q not advertised", name)
		}
	}
	if len(got) != len(expectedTools) {
		names := make([]string, 0, len(got))
		for n := range got {
			names = append(names, n)
		}
		t.Errorf("advertised %d tools, want %d: %v", len(got), len(expectedTools), names)
	}
	// The mutating / destructive actions must never be exposed.
	for _, banned := range []string{"packagist_release", "release", "github_branches_delete"} {
		if got[banned] {
			t.Errorf("mutating tool %q must not be exposed over MCP", banned)
		}
	}
}

func TestCallTool_CodeRoundTrip(t *testing.T) {
	ctx := context.Background()

	dir := t.TempDir()
	src := filepath.Join(dir, "sample.go")
	code := "package sample\n\ntype Widget struct{ Name string }\n\nfunc (w Widget) Hello() string { return w.Name }\n"
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := connectInMemory(ctx, t)
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "code",
		Arguments: map[string]any{"files": []string{src}},
	})
	if err != nil {
		t.Fatalf("tools/call code: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool reported error: %s", contentText(res))
	}
	text := contentText(res)
	if text == "" {
		t.Fatal("empty tool result content")
	}
	// The payload is the CLI's --format json output: a JSON value that names
	// the symbol we defined, followed by the one-line cost footer (which is
	// why this decodes the first value instead of unmarshalling the whole
	// payload).
	var v any
	if err := json.NewDecoder(strings.NewReader(text)).Decode(&v); err != nil {
		t.Fatalf("payload does not start with valid JSON: %v\npayload:\n%s", err, text)
	}
	if !strings.Contains(text, "Widget") {
		t.Errorf("JSON payload missing type Widget:\n%s", text)
	}
	// The cost footer must flow through the shared Run path over MCP too.
	if !strings.Contains(text, "# sf ≈") {
		t.Errorf("cost footer missing from MCP payload:\n%s", text)
	}
}

func TestCallTool_CodeDedupStubAndForce(t *testing.T) {
	ctx := context.Background()
	t.Setenv("SOFIA_LOG_DIR", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("SOFIA_SESSION_ID", "sid-mcp-dedup")
	t.Setenv("SOFIA_DEDUP_WINDOW", "180")

	dir := t.TempDir()
	src := filepath.Join(dir, "sample.go")
	code := "package sample\n\ntype Widget struct{ Name string }\n\nfunc (w Widget) Hello() string { return w.Name }\n"
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := connectInMemory(ctx, t)

	res1, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "code",
		Arguments: map[string]any{"files": []string{src}},
	})
	if err != nil {
		t.Fatalf("tools/call code (first): %v", err)
	}
	if res1.IsError {
		t.Fatalf("first call reported error: %s", contentText(res1))
	}

	res2, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "code",
		Arguments: map[string]any{"files": []string{src}},
	})
	if err != nil {
		t.Fatalf("tools/call code (second): %v", err)
	}
	text2 := contentText(res2)
	if !strings.Contains(text2, `"dedup":true`) {
		t.Fatalf("second identical call must be dedup-stubbed, got:\n%s", text2)
	}

	res3, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "code",
		Arguments: map[string]any{"files": []string{src}, "force": true},
	})
	if err != nil {
		t.Fatalf("tools/call code (force): %v", err)
	}
	text3 := contentText(res3)
	if strings.Contains(text3, `"dedup":true`) {
		t.Fatalf("force:true must bypass the dedup stub, got:\n%s", text3)
	}
	if !strings.Contains(text3, "Widget") {
		t.Errorf("forced call must return full content, got:\n%s", text3)
	}
}

func TestEnsureSessionEnv(t *testing.T) {
	t.Run("sets SOFIA_SESSION_ID when no session is present", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "")
		t.Setenv("SOFIA_SESSION_ID", "")
		ensureSessionEnv()
		if calllog.SessionID() == "" {
			t.Fatal("expected ensureSessionEnv to synthesize a session id")
		}
		if !strings.HasPrefix(os.Getenv("SOFIA_SESSION_ID"), "mcp-") {
			t.Errorf("SOFIA_SESSION_ID = %q, want an mcp-<pid>-<ts> id", os.Getenv("SOFIA_SESSION_ID"))
		}
	})
	t.Run("leaves an existing session id alone", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_SESSION_ID", "")
		t.Setenv("SOFIA_SESSION_ID", "already-set")
		ensureSessionEnv()
		if got := os.Getenv("SOFIA_SESSION_ID"); got != "already-set" {
			t.Errorf("SOFIA_SESSION_ID = %q, want unchanged already-set", got)
		}
	})
}

func TestCallTool_ErrorIsToolError(t *testing.T) {
	ctx := context.Background()
	cs := connectInMemory(ctx, t)

	// An unsupported file extension is a usage error inside code.Run; it must
	// surface as a tool error (IsError), not a protocol-level failure.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "code",
		Arguments: map[string]any{"files": []string{"/nonexistent/file.rb"}},
	})
	if err != nil {
		t.Fatalf("tools/call should not be a protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for unsupported input, got success: %s", contentText(res))
	}
}

func TestCallTool_RequiredArgValidation(t *testing.T) {
	ctx := context.Background()
	cs := connectInMemory(ctx, t)

	// "package" is a required property of composer_show; omitting it must be
	// rejected by schema validation before reaching the handler. The SDK
	// surfaces that as a tool error (IsError), not a protocol-level error.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "composer_show",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("tools/call should not be a protocol error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected schema validation error for missing required 'package', got: %s", contentText(res))
	}
}
