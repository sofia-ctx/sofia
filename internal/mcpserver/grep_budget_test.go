package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sofia-ctx/sofia/internal/common/grep"
)

func TestGrepMCPByteBudgetAndContinuation(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.php"), []byte(strings.Repeat("needle "+strings.Repeat("Ж", 100)+"\n", 80)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	client := connectInMemory(ctx, t)
	args := map[string]any{"root": root, "patterns": []string{"needle"}, "max_per_pattern": 0, "max_total": 0, "max_bytes": 1200}
	seen := 0
	for {
		args["offset"] = seen
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "grep", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		text := contentText(result)
		if result.IsError || len(text) > 1200 || !strings.Contains(text, "# sf") {
			t.Fatalf("invalid bounded MCP reply: %s", text)
		}
		var page grep.Result
		if err := json.NewDecoder(strings.NewReader(text)).Decode(&page); err != nil {
			t.Fatal(err)
		}
		for _, pattern := range page.Patterns {
			for _, h := range pattern.Hits {
				seen++
				if h.Line != seen {
					t.Fatalf("expected line %d, got %d", seen, h.Line)
				}
			}
		}
		if page.Page.NextOffset == nil {
			break
		}
		if page.Page.Shown == 0 || *page.Page.NextOffset != seen {
			t.Fatal("no progress")
		}
	}
	if seen != 80 {
		t.Fatalf("lost hits: %d", seen)
	}
	// Omitting the new fields applies the same defaults as the CLI.
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "grep", Arguments: map[string]any{"root": root, "patterns": []string{"needle"}, "max_per_pattern": 0}})
	if err != nil || result.IsError || len(contentText(result)) > grep.DefaultMaxBytes {
		t.Fatalf("default limit failed: %v", err)
	}
}
