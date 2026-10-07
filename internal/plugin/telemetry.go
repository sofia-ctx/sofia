package plugin

import (
	"encoding/json"
	"io"
	"os"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

const summaryEnv = "SOFIA_CALL_SUMMARY_FILE"
const summaryLimit = 64 * 1024

// The optional side channel never changes stdout, stderr or exit status.
// Commands suppressing argv also suppress summaries: query payloads must not
// leak back into history through an alternative channel.
func summaryFile(command *Command) *os.File {
	if command != nil && !command.ShouldLogArgs() {
		return nil
	}
	f, _ := os.CreateTemp("", "sf-summary-*.json")
	return f
}

func collectSummary(f *os.File, tracker *calllog.Tracker) {
	if f == nil {
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return
	}
	b, err := io.ReadAll(io.LimitReader(f, summaryLimit+1))
	if err != nil || len(b) > summaryLimit {
		return
	}
	var summary map[string]any
	if json.Unmarshal(b, &summary) == nil {
		tracker.SetSummary(summary)
	}
}
