package grep

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputLimitsAndContinuationInEveryFormat(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.php"), []byte("<?php\nA_one;\nA_two;\nA_three;\nB_one;\nB_two;\nB_three;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := Options{Root: root, Patterns: []string{"A_", "B_"}, CaseSensitive: true, MaxPerPattern: 2, MaxTotal: 3}
	for _, format := range []string{"json", "toon", "md"} {
		t.Run(format, func(t *testing.T) {
			opts.Format = format
			var out bytes.Buffer
			if err := Run(opts, &out); err != nil {
				t.Fatal(err)
			}
			for _, omitted := range []string{"A_three", "B_two", "B_three"} {
				if strings.Contains(out.String(), omitted) {
					t.Fatalf("unbounded %s: %s", format, out.String())
				}
			}
			if format == "json" {
				var r Result
				if err := json.Unmarshal(out.Bytes(), &r); err != nil {
					t.Fatal(err)
				}
				if r.Page.Total != 6 || r.Page.Eligible != 4 || r.Page.Shown != 3 || r.Page.NextOffset == nil || *r.Page.NextOffset != 3 {
					t.Fatalf("page: %+v", r.Page)
				}
				opts.Offset = *r.Page.NextOffset
				out.Reset()
				if err := Run(opts, &out); err != nil {
					t.Fatal(err)
				}
				r = Result{}
				if err := json.Unmarshal(out.Bytes(), &r); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), "B_two") || strings.Contains(out.String(), "B_one") || r.Page.Shown != 1 || r.Page.NextOffset != nil {
					t.Fatalf("continuation: %s", out.String())
				}
				opts.Offset = 0
			}
		})
	}
}

func TestNegativeLimitsAreErrors(t *testing.T) {
	for _, o := range []Options{{MaxTotal: -1}, {Offset: -1}, {MaxPerPattern: -1}} {
		if Run(o, &bytes.Buffer{}) == nil {
			t.Fatal("negative limit accepted")
		}
	}
}
