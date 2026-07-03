package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestStdioProtocolRoundTrip exercises the actual stdio transport end to end:
// it builds the `sf` binary, launches `sf mcp` as a subprocess, and drives it
// through the real JSON-RPC framing over the process's stdin/stdout —
// initialize (via Connect), tools/list, and a tools/call. Framing bugs that a
// pure in-memory test can't catch surface here.
func TestStdioProtocolRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the sf binary; skipped under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "sf")
	build := exec.Command(goBin, "build", "-o", bin, "github.com/sofia-ctx/sofia/cmd/sf")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sf: %v\n%s", err, out)
	}

	src := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(src, []byte("package sample\n\ntype Widget struct{ Name string }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(bin, "mcp")}, nil)
	if err != nil {
		t.Fatalf("connect to `sf mcp` over stdio (initialize failed): %v", err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list over stdio: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("tools/list returned no tools")
	}
	var haveCode bool
	for _, tool := range tools.Tools {
		if tool.Name == "code" {
			haveCode = true
		}
	}
	if !haveCode {
		t.Fatal("tools/list over stdio missing `code`")
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "code",
		Arguments: map[string]any{"files": []string{src}},
	})
	if err != nil {
		t.Fatalf("tools/call over stdio: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error over stdio: %s", stdioText(res))
	}
	text := stdioText(res)
	// Decode the leading JSON value; the payload ends with the one-line cost
	// footer, which is not part of the JSON document.
	var v any
	if err := json.NewDecoder(strings.NewReader(text)).Decode(&v); err != nil {
		t.Fatalf("stdio payload does not start with valid JSON: %v\n%s", err, text)
	}
	if !strings.Contains(text, "Widget") {
		t.Errorf("stdio payload missing type Widget:\n%s", text)
	}
}

func stdioText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
