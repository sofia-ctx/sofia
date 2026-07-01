package cc

// Exported entry points for the MCP server (internal/mcpserver).
//
// Each cc subcommand's Cobra RunE resolves a projects dir + a session
// selector and then calls an unexported run* function, writing to os.Stdout.
// The MCP layer lives in another package (so it can't reach those unexported
// functions) and must never write to os.Stdout — that stream carries the MCP
// JSON-RPC framing. These thin wrappers mirror each RunE but take an explicit
// io.Writer, reusing the same resolvers (ProjectsDir, ResolveSelector,
// parseSince, collectSessions) and run* functions the CLI uses. No analysis
// logic is duplicated here, only the arg→path glue.

import (
	"io"
	"time"
)

// sinceTime turns a human duration ("24h", "7d", "") into an absolute
// lower bound, mirroring the RunE handling (empty ⇒ zero time ⇒ no filter).
func sinceTime(since string) (time.Time, error) {
	if since == "" {
		return time.Time{}, nil
	}
	d, err := parseSince(since)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(-d), nil
}

// resolvePath resolves projects-dir + selector to a transcript path, the way
// show/resume/prompts/bash do.
func resolvePath(projectsDir, selector string) (string, error) {
	dir, err := ProjectsDir(projectsDir)
	if err != nil {
		return "", err
	}
	return ResolveSelector(dir, selector)
}

// RunLs is the `sf cc ls` entry point used by the MCP server.
func RunLs(w io.Writer, projectsDir, project, since string, limit int, format string) error {
	dir, err := ProjectsDir(projectsDir)
	if err != nil {
		return err
	}
	sinceT, err := sinceTime(since)
	if err != nil {
		return err
	}
	return runLs(dir, lsOptions{Project: project, Since: sinceT, Limit: limit, Format: format}, w)
}

// RunShow is the `sf cc show` entry point used by the MCP server.
func RunShow(w io.Writer, projectsDir, selector, format string) error {
	path, err := resolvePath(projectsDir, selector)
	if err != nil {
		return err
	}
	return runShow(path, format, w)
}

// RunResume is the `sf cc resume` entry point used by the MCP server.
func RunResume(w io.Writer, projectsDir, selector, format string) error {
	path, err := resolvePath(projectsDir, selector)
	if err != nil {
		return err
	}
	return runResume(path, format, w)
}

// RunPrompts is the `sf cc prompts` entry point used by the MCP server.
func RunPrompts(w io.Writer, projectsDir, selector, format string) error {
	path, err := resolvePath(projectsDir, selector)
	if err != nil {
		return err
	}
	return runPrompts(path, format, w)
}

// RunBash is the `sf cc bash` entry point used by the MCP server.
func RunBash(w io.Writer, projectsDir, selector, category string, minCount, limit int, full bool, format string) error {
	path, err := resolvePath(projectsDir, selector)
	if err != nil {
		return err
	}
	return runBash(path, bashOptions{Category: category, MinCount: minCount, Limit: limit, Full: full, Format: format}, w)
}

// RunCandidates is the `sf cc candidates` entry point used by the MCP server.
// With a selector it scans one session; without, it scans a project/time
// window like the CLI does.
func RunCandidates(w io.Writer, projectsDir, selector, project, since string, minCount, limit int, format string) error {
	dir, err := ProjectsDir(projectsDir)
	if err != nil {
		return err
	}
	var sessions []*Session
	if selector != "" {
		path, err := ResolveSelector(dir, selector)
		if err != nil {
			return err
		}
		s, err := Parse(path, true)
		if err != nil {
			return err
		}
		sessions = []*Session{s}
	} else {
		sinceT, err := sinceTime(since)
		if err != nil {
			return err
		}
		sessions, err = collectSessions(dir, project, sinceT, true)
		if err != nil {
			return err
		}
	}
	return runCandidates(sessions, minCount, limit, format, w)
}
