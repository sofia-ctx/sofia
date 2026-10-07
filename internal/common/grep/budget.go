package grep

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/sofia-ctx/sofia/internal/calllog"
	"github.com/sofia-ctx/sofia/pkg/emit"
)

// DefaultMaxBytes is the shared CLI/MCP ceiling for a rendered search page.
const DefaultMaxBytes = 16 * 1024
const minMaxBytes = 512

// boundedOutput selects a prefix of the existing hit page and measures the
// actual rendering, including escaping, metadata and any cost footer. Never
// cut serialized bytes or skip a hit that did not fit: next_offset addresses
// the first unreturned hit in the same capped search result.
func boundedOutput(raw *Result, opts Options) ([]byte, *Result, error) {
	full := outputPage(raw, opts)
	output, err := encodePage(full, opts.Format)
	if err != nil || opts.MaxBytes == 0 || len(output) <= opts.MaxBytes {
		return output, full, err
	}
	minimum := min(1, full.Page.Shown)
	encode := func(n int) ([]byte, *Result, error) {
		pageOpts := opts
		pageOpts.MaxTotal = n
		page := outputPage(raw, pageOpts)
		page.Page.ByteLimited = n < full.Page.Shown
		data, err := encodePage(page, opts.Format)
		return data, page, err
	}
	best, page, err := encode(minimum)
	if err != nil {
		return nil, page, err
	}
	if len(best) > opts.MaxBytes {
		limitErr := &outputLimitError{Code: "max_bytes_too_small", MaxBytes: opts.MaxBytes, RequiredBytes: len(best), Offset: opts.Offset}
		page.Page.ByteLimited = true
		if opts.Format == "json" {
			data, _ := json.Marshal(limitErr)
			return append(data, '\n'), page, limitErr
		}
		return []byte(limitErr.Error() + "\n"), page, limitErr
	}
	for low, high := minimum+1, full.Page.Shown-1; low <= high; {
		mid := low + (high-low)/2
		data, candidate, err := encode(mid)
		if err != nil {
			return nil, candidate, err
		}
		if len(data) <= opts.MaxBytes {
			best, page = data, candidate
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return best, page, nil
}

// required_bytes is sufficient to retry this same offset with one complete
// hit (or the metadata-only result). No cursor advances on this error.
type outputLimitError struct {
	Code          string `json:"error"`
	MaxBytes      int    `json:"max_bytes"`
	RequiredBytes int    `json:"required_bytes"`
	Offset        int    `json:"offset"`
}

func (e *outputLimitError) Error() string {
	return fmt.Sprintf("max-bytes=%d cannot fit the next complete result; retry --offset %d --max-bytes %d (or --max-bytes 0)", e.MaxBytes, e.Offset, e.RequiredBytes)
}

func encodePage(page *Result, format string) ([]byte, error) {
	var output bytes.Buffer
	counter := &calllog.Counter{W: &output}
	switch format {
	case "", "toon":
		renderTOON(counter, page, 0)
	case "md":
		renderMarkdown(counter, page, 0)
	case "json":
		if err := renderJSON(counter, page); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown format %q (use toon|md|json)", format)
	}
	emit.FooterFor(counter, format, counter.Tokens, 0)
	return output.Bytes(), nil
}
