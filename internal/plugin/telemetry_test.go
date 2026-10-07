package plugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginSummaryIsCollectedOnceAndPrivateCommandsSuppressIt(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[private], func(t *testing.T) {
			isolate(t)
			logDir := t.TempDir()
			t.Setenv("SOFIA_LOG_DIR", logDir)
			// A nested invocation must not inherit another command's summary path.
			inherited := filepath.Join(t.TempDir(), "inherited.json")
			if err := os.WriteFile(inherited, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(summaryEnv, inherited)
			script := filepath.Join(t.TempDir(), "plugin")
			body := "#!/bin/sh\nif [ -n \"$SOFIA_CALL_SUMMARY_FILE\" ]; then\n printf '%s' '{\"inputs\":[\"Widget\"],\"shown\":1}' > \"$SOFIA_CALL_SUMMARY_FILE\"\nfi\nprintf 'answer\\n'\n"
			if err := os.WriteFile(script, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			d := Descriptor{Name: "example", Exec: script, Manifest: Manifest{Version: "0.9.0"}}
			logArgs := !private
			var out bytes.Buffer
			if err := Invoke(context.Background(), InvokeRequest{Descriptor: d, Command: &Command{Path: "inspect", LogArgs: &logArgs}, Stdout: &out, Stderr: &bytes.Buffer{}}); err != nil {
				t.Fatal(err)
			}
			lines := logLines(t, logDir)
			if len(lines) != 1 || out.String() != "answer\n" {
				t.Fatalf("duplicate telemetry or changed output: %+v / %s", lines, out.String())
			}
			if private && len(lines[0].Summary) != 0 {
				t.Fatalf("private metadata leaked: %+v", lines[0])
			}
			if !private && lines[0].Summary["shown"] != float64(1) {
				t.Fatalf("missing summary: %+v", lines[0])
			}
			if lines[0].PluginVersion != "0.9.0" {
				t.Fatalf("version: %+v", lines[0])
			}
			b, err := os.ReadFile(inherited)
			if err != nil || string(b) != "untouched" {
				t.Fatalf("inherited path modified: %s %v", b, err)
			}
		})
	}
}
