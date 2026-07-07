package mcpserver

import (
	"bytes"
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sofia-ctx/sofia/internal/cc"
	"github.com/sofia-ctx/sofia/internal/common/changed"
	"github.com/sofia-ctx/sofia/internal/common/code"
	"github.com/sofia-ctx/sofia/internal/common/composer"
	"github.com/sofia-ctx/sofia/internal/common/github"
	"github.com/sofia-ctx/sofia/internal/common/grep"
	"github.com/sofia-ctx/sofia/internal/common/packagist"
	"github.com/sofia-ctx/sofia/internal/common/vue"
)

// ---- input schemas (one struct per MCP tool) --------------------------------
//
// Field/tag conventions are documented in the package doc (mcpserver.go):
// positional CLI args become intent-named params, flags become snake_case
// params, no-`omitempty` ⇒ required, pointer types carry non-zero CLI
// defaults resolved via orInt/orBool.

type codeInput struct {
	Files    []string `json:"files" jsonschema:"source files, directories, or glob patterns to summarise structurally (.go/.php/.ts/.tsx/.vue); a directory expands recursively (vendor/node_modules/.git and friends skipped); pass several to summarise together (capped at 250 expanded files)"`
	Symbol   string   `json:"symbol,omitempty" jsonschema:"with exactly one file, slice this symbol's full source (func/type/const/var, or Recv.Method / Class::method) instead of summarising the file (Go/PHP only); for more than one, use symbols instead"`
	Symbols  []string `json:"symbols,omitempty" jsonschema:"with exactly one file, slice these symbols' full source in one call instead of summarising the file (Go/PHP only); a symbol that isn't found doesn't fail the call, it's just noted as missing"`
	Exported bool     `json:"exported,omitempty" jsonschema:"show only exported/public symbols"`
	API      bool     `json:"api,omitempty" jsonschema:"PHP: effective public surface (own + trait + inherited methods); implies exported"`
	Force    bool     `json:"force,omitempty" jsonschema:"re-fetch even if this exact call was answered moments ago (bypasses the 'already returned' dedup stub)"`
}

type grepInput struct {
	Patterns      []string `json:"patterns" jsonschema:"search patterns; literal substrings by default"`
	Root          string   `json:"root,omitempty" jsonschema:"directory to search (default: current working directory)"`
	Regex         bool     `json:"regex,omitempty" jsonschema:"treat patterns as Go regular expressions; overrides word"`
	Word          bool     `json:"word,omitempty" jsonschema:"literal mode only: require a whole-word match"`
	Case          *bool    `json:"case,omitempty" jsonschema:"case-sensitive search (default true)"`
	Exts          []string `json:"exts,omitempty" jsonschema:"file extensions to include, e.g. [\"php\",\"ts\",\"vue\"]; empty = all"`
	IgnoreDirs    []string `json:"ignore_dirs,omitempty" jsonschema:"extra directory names to skip on top of the defaults (vendor, node_modules, …)"`
	MaxPerPattern *int     `json:"max_per_pattern,omitempty" jsonschema:"limit hits per pattern (0 = unlimited; default 30)"`
}

type changedInput struct {
	Range     string `json:"range,omitempty" jsonschema:"git revision or range (e.g. HEAD~3, main..HEAD); empty = working tree vs HEAD, incl. untracked"`
	Root      string `json:"root,omitempty" jsonschema:"git repo dir (default: current working directory)"`
	Staged    bool   `json:"staged,omitempty" jsonschema:"only staged changes (vs HEAD)"`
	NoSymbols bool   `json:"no_symbols,omitempty" jsonschema:"skip touched-symbol extraction (files + churn only)"`
}

type ccLsInput struct {
	Project     string `json:"project,omitempty" jsonschema:"filter by project (substring of dir name, project label, or cwd)"`
	Since       string `json:"since,omitempty" jsonschema:"only sessions active since a duration, e.g. 30m, 24h, 7d"`
	Limit       *int   `json:"limit,omitempty" jsonschema:"max sessions to list (0 = unlimited; default 30)"`
	ProjectsDir string `json:"projects_dir,omitempty" jsonschema:"Claude Code projects root (overrides $CC_PROJECTS_DIR)"`
}

// ccSessionInput is shared by show/resume/prompts (selector-only).
type ccSessionInput struct {
	Session     string `json:"session,omitempty" jsonschema:"session selector: omitted or 'last' = most recent anywhere; an id prefix; a project name; or a transcript path"`
	ProjectsDir string `json:"projects_dir,omitempty" jsonschema:"Claude Code projects root (overrides $CC_PROJECTS_DIR)"`
}

type ccBashInput struct {
	Session     string `json:"session,omitempty" jsonschema:"session selector: 'last' / id prefix / project / transcript path"`
	Category    string `json:"category,omitempty" jsonschema:"filter to one category: search|read|git|test|build|db|fs|other"`
	MinCount    *int   `json:"min_count,omitempty" jsonschema:"only commands run at least this many times (default 1)"`
	Limit       *int   `json:"limit,omitempty" jsonschema:"max commands, by frequency (0 = all; default 30)"`
	Full        bool   `json:"full,omitempty" jsonschema:"show full commands instead of truncating to one line"`
	ProjectsDir string `json:"projects_dir,omitempty" jsonschema:"Claude Code projects root (overrides $CC_PROJECTS_DIR)"`
}

type ccCandidatesInput struct {
	Session     string `json:"session,omitempty" jsonschema:"scan a single session (id prefix / project / path); omit to scan many"`
	Project     string `json:"project,omitempty" jsonschema:"filter by project when scanning many sessions"`
	Since       string `json:"since,omitempty" jsonschema:"only sessions active since a duration, e.g. 24h, 7d"`
	MinCount    *int   `json:"min_count,omitempty" jsonschema:"minimum repeats for repeated_commands / repeated_reads (default 2)"`
	Limit       *int   `json:"limit,omitempty" jsonschema:"max rows per repeated_* section (0 = unlimited; default 20)"`
	ProjectsDir string `json:"projects_dir,omitempty" jsonschema:"Claude Code projects root (overrides $CC_PROJECTS_DIR)"`
}

type composerLsInput struct {
	Root string `json:"root,omitempty" jsonschema:"tree to scan for composer.json files (default: current working directory)"`
}

type composerShowInput struct {
	Package string `json:"package" jsonschema:"package name, name suffix, dir basename, or path to the package dir / composer.json"`
	Root    string `json:"root,omitempty" jsonschema:"tree to search (default: current working directory)"`
}

type composerCheckInput struct {
	Package string `json:"package,omitempty" jsonschema:"only this package (name suffix or dir basename); empty = every package under root"`
	Root    string `json:"root,omitempty" jsonschema:"tree to scan (default: current working directory)"`
}

type packagistStatusInput struct {
	Root    string `json:"root,omitempty" jsonschema:"tree to scan (default: current working directory)"`
	Offline bool   `json:"offline,omitempty" jsonschema:"skip Packagist/remote probes (local git tags only)"`
}

type githubCIInput struct {
	Package string `json:"package,omitempty" jsonschema:"package name / dir basename / path resolved under root; empty = current repo"`
	Root    string `json:"root,omitempty" jsonschema:"tree to resolve a package under (default: current working directory)"`
	Limit   *int   `json:"limit,omitempty" jsonschema:"max runs to list (default 5)"`
}

type githubPRInput struct {
	Limit *int `json:"limit,omitempty" jsonschema:"max PRs to fetch per search dimension (authored / review-requested / owned; default 30)"`
}

type githubBranchesInput struct {
	Refs *int `json:"refs,omitempty" jsonschema:"max branches to inspect per repo (default 50)"`
}

type vueRoutesInput struct {
	File string `json:"file,omitempty" jsonschema:"explicit vue-router config file (overrides the tree search)"`
	Root string `json:"root,omitempty" jsonschema:"tree to search for router/index.ts (default: current working directory)"`
}

// registerTools wires every public-safe Context Provider onto the server.
func registerTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "code",
		Description: "Structural summary of source files without bodies (Go/PHP/TS/Vue) — a cheap alternative to reading whole files; or slice one or more symbols' full source in one call. JSON payload. Batch several files (or several symbols) into ONE call — one call per file/symbol wastes agent turns. For a single file under ~8 KB, or when you need most of one file's bodies, plain file reading is cheaper than structural round-trips. Small files come back raw automatically — never worse than reading the file. Repeating an identical call within a few minutes returns a short 'already returned' stub; pass force:true only when you genuinely need the content again.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in codeInput) (*mcp.CallToolResult, any, error) {
		symbols := in.Symbols
		if in.Symbol != "" {
			symbols = append([]string{in.Symbol}, symbols...)
		}
		var buf bytes.Buffer
		if err := code.Run(code.Options{
			Inputs:       in.Files,
			Symbols:      symbols,
			ExportedOnly: in.Exported,
			API:          in.API,
			Force:        in.Force,
			Format:       jsonFormat,
		}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "grep",
		Description: "Search a project tree and report each hit with its enclosing function/class/block. Literal, whole-word, or regex. JSON payload. Use it to locate, then follow up with a single batched code call covering the files that matter.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in grepInput) (*mcp.CallToolResult, any, error) {
		opts := grep.Options{
			Root:          in.Root,
			Patterns:      in.Patterns,
			CaseSensitive: orBool(in.Case, true),
			WordBound:     in.Word,
			Regex:         in.Regex,
			Exts:          in.Exts,
			ExtraIgnore:   in.IgnoreDirs,
			MaxPerPattern: orInt(in.MaxPerPattern, 30),
			Format:        jsonFormat,
		}
		if opts.Regex {
			opts.WordBound = false // --regex overrides --word, as in the CLI
		}
		var buf bytes.Buffer
		if err := grep.Run(opts, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "changed",
		Description: "Classified summary of a git diff: per file its status, churn, category, language, and touched symbols. JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in changedInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := changed.Run(changed.Options{
			Root:    in.Root,
			Range:   in.Range,
			Staged:  in.Staged,
			Symbols: !in.NoSymbols,
			Format:  jsonFormat,
		}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cc_ls",
		Description: "Index Claude Code sessions across projects (newest first) with per-session metrics. JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ccLsInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := cc.RunLs(&buf, in.ProjectsDir, in.Project, in.Since, orInt(in.Limit, 30), jsonFormat); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cc_show",
		Description: "One-screen digest of a single Claude Code session: meta, token usage, prompts, tool histogram, files touched, heaviest results. JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ccSessionInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := cc.RunShow(&buf, in.ProjectsDir, in.Session, jsonFormat); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cc_resume",
		Description: "Tiny resume brief for a session (goal, latest ask, next step, working-set files) so a fresh session restarts cheaply. JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ccSessionInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := cc.RunResume(&buf, in.ProjectsDir, in.Session, jsonFormat); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cc_prompts",
		Description: "The genuine human turns of a session, verbatim and in order (system-reminders and tool batches filtered out). JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ccSessionInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := cc.RunPrompts(&buf, in.ProjectsDir, in.Session, jsonFormat); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cc_bash",
		Description: "Shell commands a session ran, deduplicated with a frequency count and a category. JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ccBashInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := cc.RunBash(&buf, in.ProjectsDir, in.Session, in.Category,
			orInt(in.MinCount, 1), orInt(in.Limit, 30), in.Full, jsonFormat); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "cc_candidates",
		Description: "Find tool candidates across sessions: heavy tools, repeated commands, repeated reads — the recurring, token-expensive operations. JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in ccCandidatesInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := cc.RunCandidates(&buf, in.ProjectsDir, in.Session, in.Project, in.Since,
			orInt(in.MinCount, 2), orInt(in.Limit, 20), jsonFormat); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "composer_ls",
		Description: "One digest row per PHP package across a tree (name, version, type, php, phpstan, scripts, deps). JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in composerLsInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := composer.Run(composer.Options{Root: in.Root, Format: jsonFormat}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "composer_show",
		Description: "Full metadata for a single PHP package: version, type, php, phpstan, namespace, scripts with commands, all deps. JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in composerShowInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := composer.RunShow(composer.ShowOptions{Root: in.Root, Target: in.Package, Format: jsonFormat}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "composer_check",
		Description: "Run each package's own composer 'check' gate (test + phpstan + cs) and report a compact pass/fail per package. JSON payload.",
		Annotations: annotations(true),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in composerCheckInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := composer.RunCheck(composer.CheckOptions{Root: in.Root, Target: in.Package, Format: jsonFormat}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "packagist_status",
		Description: "Per-package release health: latest local git tag, whether it is pushed, and the version published on Packagist. JSON payload.",
		Annotations: annotations(true),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in packagistStatusInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := packagist.Run(packagist.Options{Root: in.Root, Offline: in.Offline, Format: jsonFormat}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "github_ci",
		Description: "Latest GitHub Actions runs for a repo (or per package across a tree), via the gh CLI. Non-blocking. JSON payload.",
		Annotations: annotations(true),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in githubCIInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := github.RunCI(github.Options{
			Root:   in.Root,
			Target: in.Package,
			Limit:  orInt(in.Limit, 5),
			Format: jsonFormat,
			// Watch/Timeout deliberately not exposed: a request/response tool
			// must not block for minutes on CI.
		}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "github_pr",
		Description: "Your open pull requests across all repos (authored, review-requested, on your public repos) with a CI/review rollup. JSON payload.",
		Annotations: annotations(true),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in githubPRInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := github.RunPR(github.PROptions{Limit: orInt(in.Limit, 30), Format: jsonFormat}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "github_branches",
		Description: "Non-default branches across your own repos with merged/closed/stale verdicts (report-only; deletion is CLI-only). JSON payload.",
		Annotations: annotations(true),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in githubBranchesInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := github.RunBranches(github.BranchOptions{
			Refs:   orInt(in.Refs, 50),
			Delete: "", // report-only over MCP; the destructive --delete is not mapped
			Format: jsonFormat,
		}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "vue_routes",
		Description: "Flat, depth-resolved route map (path/name/component/meta) parsed from a vue-router config. JSON payload.",
		Annotations: annotations(false),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in vueRoutesInput) (*mcp.CallToolResult, any, error) {
		var buf bytes.Buffer
		if err := vue.Run(vue.Options{Root: in.Root, File: in.File, Format: jsonFormat}, &buf); err != nil {
			return nil, nil, err
		}
		return textResult(&buf), nil, nil
	})
}
