package grep

import (
	"fmt"
	"io"
)

// Page describes a stable window after applying per-pattern limits. Total
// still counts every match; Eligible excludes those omitted by those limits.
type Page struct {
	Total      int  `json:"total"`
	Eligible   int  `json:"eligible"`
	Shown      int  `json:"shown"`
	Truncated  int  `json:"truncated"`
	Offset     int  `json:"offset"`
	NextOffset *int `json:"next_offset,omitempty"`
}

func outputPage(r *Result, opts Options) *Result {
	out := *r
	out.Patterns = make([]*PatternResult, 0, len(r.Patterns))
	out.Page = &Page{Offset: opts.Offset}
	index := 0
	for _, pr := range r.Patterns {
		p := *pr
		p.Total = len(pr.Hits)
		p.Hits = []Hit{}
		out.Page.Total += p.Total
		end := p.Total
		if opts.MaxPerPattern > 0 && end > opts.MaxPerPattern {
			end = opts.MaxPerPattern
		}
		out.Page.Eligible += end
		for _, h := range pr.Hits[:end] {
			if index >= opts.Offset && (opts.MaxTotal == 0 || out.Page.Shown < opts.MaxTotal) {
				p.Hits = append(p.Hits, h)
				out.Page.Shown++
			}
			index++
		}
		p.Shown = len(p.Hits)
		p.Truncated = p.Total - p.Shown
		out.Patterns = append(out.Patterns, &p)
	}
	out.Page.Truncated = out.Page.Total - out.Page.Shown
	if opts.Offset+out.Page.Shown < out.Page.Eligible {
		next := opts.Offset + out.Page.Shown
		out.Page.NextOffset = &next
	}
	return &out
}

func writePage(w io.Writer, p *Page) {
	if p == nil {
		return
	}
	fmt.Fprintf(w, "page{total=%d,eligible=%d,shown=%d,truncated=%d,offset=%d}", p.Total, p.Eligible, p.Shown, p.Truncated, p.Offset)
	if p.NextOffset != nil {
		fmt.Fprintf(w, " next: --offset %d", *p.NextOffset)
	}
	fmt.Fprintln(w)
}
