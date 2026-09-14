package main

import (
	"errors"
	"fmt"
	"testing"
)

type testExitError int

func (e testExitError) Error() string { return fmt.Sprintf("exit %d", e) }
func (e testExitError) ExitCode() int { return int(e) }

type testReportedError bool

func (e testReportedError) Error() string         { return "reported" }
func (e testReportedError) AlreadyReported() bool { return bool(e) }

func TestStatusCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"ordinary", errors.New("failed"), 1},
		{"usage", testExitError(64), 64},
		{"wrapped", fmt.Errorf("wrapped: %w", testExitError(23)), 23},
		{"zero", testExitError(0), 1},
		{"negative", testExitError(-1), 1},
		{"too-large", testExitError(256), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusCode(tt.err); got != tt.want {
				t.Fatalf("statusCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAlreadyReported(t *testing.T) {
	if alreadyReported(errors.New("ordinary")) || alreadyReported(testReportedError(false)) || !alreadyReported(testReportedError(true)) {
		t.Fatal("reported error classification failed")
	}
}
