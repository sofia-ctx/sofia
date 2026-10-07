package grep

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func budgetFixture() *Result {
	r := &Result{}
	for _, pattern := range []string{"alpha", "beta"} {
		p := &PatternResult{Pattern: pattern, Files: 1}
		for i := 0; i < 12; i++ {
			p.Hits = append(p.Hits, Hit{File: pattern + ".php", Line: i + 1, Col: 2, Enclosing: "Class.method", Text: "x" + strings.Repeat("Ж🙂\"\\", 40)})
		}
		r.Patterns = append(r.Patterns, p)
	}
	return r
}

func TestBytePagesAreValidCompleteAndResumable(t *testing.T) {
	t.Setenv("SOFIA_FOOTER", "")
	for _, format := range []string{"toon", "md", "json"} {
		t.Run(format, func(t *testing.T) {
			raw := budgetFixture()
			opts := Options{Format: format, MaxTotal: 9, MaxBytes: 1500}
			seen := []string{}
			for count := 0; ; count++ {
				if count > 24 {
					t.Fatal("pagination made no progress")
				}
				out, page, err := boundedOutput(raw, opts)
				if err != nil || len(out) > opts.MaxBytes || !utf8.Valid(out) {
					t.Fatalf("page: %d bytes, %v\n%s", len(out), err, out)
				}
				if page.Page.Total != 24 || page.Page.Eligible != 24 || page.Page.Shown == 0 {
					t.Fatalf("counts: %+v", page.Page)
				}
				if count == 0 && !page.Page.ByteLimited {
					t.Fatal("missing byte_limited marker")
				}
				if format == "json" {
					var parsed Result
					if err := json.Unmarshal(out, &parsed); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(parsed.Page, page.Page) {
						t.Fatal("rendered metadata differs from selected page")
					}
				} else if !bytes.Contains(out, []byte("# sf")) {
					t.Fatal("footer was not included in measured output")
				}
				for _, p := range page.Patterns {
					for _, hit := range p.Hits {
						if hit.Text != raw.Patterns[0].Hits[0].Text {
							t.Fatal("hit content was cut")
						}
						seen = append(seen, fmt.Sprintf("%s:%d", hit.File, hit.Line))
					}
				}
				if page.Page.NextOffset == nil {
					break
				}
				if *page.Page.NextOffset != opts.Offset+page.Page.Shown {
					t.Fatal("cursor skipped a hit")
				}
				opts.Offset = *page.Page.NextOffset
			}
			want := []string{}
			for _, p := range raw.Patterns {
				for _, hit := range p.Hits {
					want = append(want, fmt.Sprintf("%s:%d", hit.File, hit.Line))
				}
			}
			if !reflect.DeepEqual(seen, want) {
				t.Fatalf("lost/duplicated/reordered hits: %v", seen)
			}
		})
	}
}

func TestOversizedRecordAndMetadataErrorsCanBeRetried(t *testing.T) {
	for _, raw := range []*Result{
		{Patterns: []*PatternResult{{Pattern: "long", Hits: []Hit{{File: "a.php", Line: 1, Text: strings.Repeat("x", 5000)}}}}},
		{Empty: []string{strings.Repeat("Ж", 5000)}},
	} {
		opts := Options{Format: "json", MaxBytes: 512}
		out, _, err := boundedOutput(raw, opts)
		var limitErr *outputLimitError
		if !errors.As(err, &limitErr) || len(out) > 512 || !json.Valid(out) {
			t.Fatalf("bad bounded error: %v %s", err, out)
		}
		var body outputLimitError
		if err := json.Unmarshal(out, &body); err != nil || body.Offset != 0 || body.RequiredBytes <= 512 {
			t.Fatalf("bad retry metadata: %+v %v", body, err)
		}
		opts.MaxBytes = body.RequiredBytes
		out, _, err = boundedOutput(raw, opts)
		if err != nil || len(out) > opts.MaxBytes || !json.Valid(out) {
			t.Fatalf("suggested limit failed: %v", err)
		}
		opts.MaxBytes = 0
		out, _, err = boundedOutput(raw, opts)
		if err != nil || len(out) <= 512 {
			t.Fatal("zero did not disable byte cap")
		}
	}
}

func TestGrepRunEnforcesBytesAndValidatesLimits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.php"), []byte(strings.Repeat("needle "+strings.Repeat("Ж", 100)+"\n", 30)), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(Options{Root: root, Patterns: []string{"needle"}, Format: "json", MaxBytes: 1000}, &out); err != nil || out.Len() > 1000 || !json.Valid(out.Bytes()) {
		t.Fatalf("Run: %v, %d bytes", err, out.Len())
	}
	for _, limit := range []int{-1, 1, 511} {
		if err := Run(Options{MaxBytes: limit}, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %d", limit)
		}
	}
	flag := NewCommand().Flags().Lookup("max-bytes")
	if flag == nil || flag.DefValue != "16384" {
		t.Fatalf("unexpected CLI default: %v", flag)
	}
}
