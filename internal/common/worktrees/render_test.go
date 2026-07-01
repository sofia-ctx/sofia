package worktrees

import (
	"bytes"
	"strings"
	"testing"
)

func sampleResult() *Result {
	run := true
	return &Result{
		Www: "/www",
		Forks: []Fork{{
			Project: "myapp", Slug: "s2", Branch: "wt/s2", Dir: "/www/wt/myapp-s2",
			Running: &run, Health: "ok", URL: "https://myapp-s2.localhost:4761",
			FrontURL: "http://myapp-s2.localhost:5283", Age: "42 minutes ago",
		}},
	}
}

func TestRenderTOONIncludesFront(t *testing.T) {
	var b bytes.Buffer
	renderTOON(&b, sampleResult())
	out := b.String()
	if !strings.Contains(out, ",url,front,age}") {
		t.Errorf("TOON header missing front column:\n%s", out)
	}
	if !strings.Contains(out, "http://myapp-s2.localhost:5283") {
		t.Errorf("TOON row missing front url:\n%s", out)
	}
}

func TestRenderMarkdownIncludesFront(t *testing.T) {
	var b bytes.Buffer
	renderMarkdown(&b, sampleResult())
	out := b.String()
	if !strings.Contains(out, "| URL | Front | Age |") {
		t.Errorf("Markdown header missing Front column:\n%s", out)
	}
	if !strings.Contains(out, "http://myapp-s2.localhost:5283") {
		t.Errorf("Markdown row missing front url:\n%s", out)
	}
}

func TestRenderJSONIncludesFront(t *testing.T) {
	var b bytes.Buffer
	if err := renderJSON(&b, sampleResult()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"front_url": "http://myapp-s2.localhost:5283"`) {
		t.Errorf("JSON missing front_url:\n%s", b.String())
	}
}
