// Package cc reads Claude Code session transcripts (the JSONL files under
// ~/.claude/projects/<encoded-path>/<session-id>.jsonl) and turns them into
// compact, TOON-friendly digests. It is a Context Provider: a single 2-4 MB
// transcript costs ~hundreds of thousands of tokens to read raw, while a
// `sf cc show` digest is a few hundred — the per-session intent, tool
// histogram, real token usage, files touched, and token-heavy results.
//
// Two views are exposed:
//   - `sf cc ls`   — an index of sessions across all projects.
//   - `sf cc show` — a one-screen digest of a single session.
//
// Unlike `sf history` (which reads sofia's *own* call log), cc reads the
// agent's transcripts, so the two are siblings: one measures the tools, the
// other measures the sessions that drove them.
package cc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/envfile"
	"github.com/sofia-ctx/sofia/internal/tokens"
)

// ProjectsDirKey is the env/.env key holding the Claude Code projects root.
const ProjectsDirKey = "CC_PROJECTS_DIR"

// fatResult heuristics: a tool result above this token estimate is "fat"
// (a candidate for compaction); we keep the top N across the session.
const (
	fatResultMinTokens = 1500
	fatResultTopN      = 15
	promptMaxLen       = 200  // display truncation in `show` (prompts cmd shows full)
	maxPrompts         = 15   // display cap in `show` (+N more → sf cc prompts)
	maxPromptsStored   = 2000 // sanity cap on prompts kept per session
)

// NewCommand returns the `cc` command group (ls, show), wired into the root
// command tree the same way every other tool group is in internal/cli.
func NewCommand() *cobra.Command {
	g := &cobra.Command{
		Use:   "cc",
		Short: "Read & analyse Claude Code session transcripts",
		Long: `cc summarises Claude Code session transcripts (~/.claude/projects/**.jsonl)
into compact TOON digests instead of reading the raw multi-MB JSONL.

  sf cc ls                       index sessions across all projects
  sf cc show last                digest the most recent session
  sf cc show 6bd96fc7            digest by session-id prefix
  sf cc show myapp               digest the latest session of a project
  sf cc resume myapp             tiny brief to restart a session cheaply
  sf cc value                    your own weekly $ delta + token-type breakdown

Projects root resolves from --projects-dir, then $CC_PROJECTS_DIR, then
~/.claude/projects.`,
	}
	g.AddCommand(newLsCommand())
	g.AddCommand(newShowCommand())
	g.AddCommand(newResumeCommand())
	g.AddCommand(newPromptsCommand())
	g.AddCommand(newBashCommand())
	g.AddCommand(newCandidatesCommand())
	g.AddCommand(newValueCommand())
	return g
}

// collectSessions parses every session under projectsDir, optionally
// filtered by a project substring (matched against dir name, project
// label, or cwd) and recency. detail controls whether the heavy per-call
// slices (Bash/Files/FatResults) are gathered.
func collectSessions(projectsDir, project string, since time.Time, detail bool) ([]*Session, error) {
	files, err := listSessions(projectsDir)
	if err != nil {
		return nil, err
	}
	proj := strings.ToLower(project)
	out := make([]*Session, 0, len(files))
	for _, f := range files {
		if !since.IsZero() && f.ModTime.Before(since) {
			continue
		}
		s, err := Parse(f.Path, detail)
		if err != nil {
			continue
		}
		if proj != "" &&
			!strings.Contains(strings.ToLower(f.DirName), proj) &&
			!strings.Contains(strings.ToLower(s.Project), proj) &&
			!strings.Contains(strings.ToLower(s.Cwd), proj) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// ProjectsDir resolves the Claude Code projects root: flag, then
// $CC_PROJECTS_DIR (.env or process env), then ~/.claude/projects. The
// default is correct on virtually every install, so cc never prompts.
func ProjectsDir(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env, _ := envfile.Load(envPath()); env[ProjectsDirKey] != "" {
		return env[ProjectsDirKey], nil
	}
	if v := os.Getenv(ProjectsDirKey); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home dir for default projects root: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// envPath is the optional .env path for cc, next to the running binary.
// cc works without it (the default projects dir is almost always right),
// but honouring an .env keeps cc consistent with the rest of the toolkit.
func envPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ".env"
	}
	dir := filepath.Dir(exe)
	for i := 0; i < 8; i++ {
		if filepath.Base(dir) == "bin" {
			return filepath.Join(dir, "common", "cc", ".env")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(filepath.Dir(exe), ".env")
}

// SessionFile is a discovered transcript on disk, before parsing.
type SessionFile struct {
	Path    string    // absolute path to the .jsonl
	Stem    string    // file name without .jsonl (the session UUID)
	DirName string    // encoded project dir name, e.g. -home-user-www-myapp
	ModTime time.Time // file mtime — cheap recency signal
	Size    int64
}

// listSessions discovers every *.jsonl transcript under projectsDir, one
// directory level deep (the Claude Code layout). Sidecar files in nested
// dirs are ignored.
func listSessions(projectsDir string) ([]SessionFile, error) {
	dirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}
	var out []SessionFile
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		projDir := filepath.Join(projectsDir, d.Name())
		files, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			out = append(out, SessionFile{
				Path:    filepath.Join(projDir, f.Name()),
				Stem:    strings.TrimSuffix(f.Name(), ".jsonl"),
				DirName: d.Name(),
				ModTime: info.ModTime(),
				Size:    info.Size(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// ResolveSelector maps a human selector to a single transcript path:
//
//	""/"last"      → most recently modified session anywhere
//	*.jsonl / path → that file directly
//	<uuid-prefix>  → newest session whose file name starts with the selector
//	<project>      → newest session whose project dir name contains the selector
//
// Resolution mirrors how xref resolves constant names: try the precise
// interpretation first, fall back to the fuzzy one.
func ResolveSelector(projectsDir, sel string) (string, error) {
	sel = strings.TrimSpace(sel)
	if strings.HasSuffix(sel, ".jsonl") || strings.ContainsAny(sel, "/\\") {
		if _, err := os.Stat(sel); err == nil {
			return sel, nil
		}
		return "", fmt.Errorf("session file not found: %s", sel)
	}
	files, err := listSessions(projectsDir)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no sessions found under %s", projectsDir)
	}
	if sel == "" || sel == "last" {
		return files[0].Path, nil // already sorted newest-first
	}
	low := strings.ToLower(sel)
	for _, f := range files { // by uuid prefix (files are newest-first)
		if strings.HasPrefix(strings.ToLower(f.Stem), low) {
			return f.Path, nil
		}
	}
	for _, f := range files { // by project dir substring
		if strings.Contains(strings.ToLower(f.DirName), low) {
			return f.Path, nil
		}
	}
	return "", fmt.Errorf("no session matched %q (tried 'last', uuid prefix, project name)", sel)
}

// FileTouch counts how a path was accessed within a session.
type FileTouch struct {
	Path   string `json:"path"`
	Reads  int    `json:"reads"`
	Edits  int    `json:"edits"`
	Writes int    `json:"writes"`
}

// FatResult is a single token-heavy tool result — a compaction candidate.
type FatResult struct {
	Tokens int64  `json:"tokens"`
	Tool   string `json:"tool"`
	Brief  string `json:"brief"`
}

// PR is a pull request surfaced in the transcript.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Session is the parsed digest of one transcript.
type Session struct {
	Path      string    `json:"path"`
	ID        string    `json:"id"`      // short id (uuid prefix)
	FullID    string    `json:"full_id"` // full session UUID
	Project   string    `json:"project"` // basename of cwd
	Cwd       string    `json:"cwd"`
	Branch    string    `json:"branch"`
	Model     string    `json:"model"`
	Version   string    `json:"version"`
	Title     string    `json:"title"`
	Start     time.Time `json:"-"` // surfaced as RFC3339 strings by the json renderer
	End       time.Time `json:"-"`
	Messages  int       `json:"messages"`
	SizeBytes int64     `json:"size_bytes"`

	UserPrompts []string `json:"user_prompts"`
	LastText    string   `json:"last_text,omitempty"` // last assistant narrative — the "next step"

	ToolCalls        map[string]int   `json:"tool_calls"`
	ToolResultTokens map[string]int64 `json:"tool_result_tokens"`
	Bash             []string         `json:"-"`
	Files            []FileTouch      `json:"files"`
	FatResults       []FatResult      `json:"fat_results"`

	OutputTokens      int64 `json:"output_tokens"`
	InputTokens       int64 `json:"input_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	CacheCreateTokens int64 `json:"cache_create_tokens"`
	DurationMs        int64 `json:"duration_ms"`

	PRs []PR `json:"prs"`
}

// ToolCount is one row of the tool histogram, sorted for rendering.
type ToolCount struct {
	Tool         string `json:"tool"`
	Calls        int    `json:"calls"`
	ResultTokens int64  `json:"result_tokens"`
}

// SortedTools returns the tool histogram ordered by call count desc.
func (s *Session) SortedTools() []ToolCount {
	out := make([]ToolCount, 0, len(s.ToolCalls))
	for name, n := range s.ToolCalls {
		out = append(out, ToolCount{Tool: name, Calls: n, ResultTokens: s.ToolResultTokens[name]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Tool < out[j].Tool
	})
	return out
}

// BashCategories returns counts of bash commands per category, desc.
func (s *Session) BashCategories() []CategoryCount {
	counts := map[string]int{}
	for _, c := range s.Bash {
		counts[Categorize(c)]++
	}
	out := make([]CategoryCount, 0, len(counts))
	for cat, n := range counts {
		out = append(out, CategoryCount{Category: cat, Calls: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Category < out[j].Category
	})
	return out
}

// CategoryCount is one row of the bash-category breakdown. Unique counts
// distinct commands in the category (set by `bash`; 0/omitted in `show`).
type CategoryCount struct {
	Category string `json:"category"`
	Calls    int    `json:"calls"`
	Unique   int    `json:"unique,omitempty"`
}

// Span returns the wall-clock duration of the session.
func (s *Session) Span() time.Duration {
	if s.Start.IsZero() || s.End.IsZero() {
		return 0
	}
	return s.End.Sub(s.Start)
}

// --- raw JSONL shapes (only the fields cc reads) -------------------------

type rawEntry struct {
	Type         string          `json:"type"`
	Cwd          string          `json:"cwd"`
	GitBranch    string          `json:"gitBranch"`
	Version      string          `json:"version"`
	SessionID    string          `json:"sessionId"`
	Timestamp    string          `json:"timestamp"`
	DurationMs   int64           `json:"durationMs"`
	IsMeta       bool            `json:"isMeta"`
	IsSidechain  bool            `json:"isSidechain"`
	AiTitle      string          `json:"aiTitle"`
	MessageCount int             `json:"messageCount"`
	PrNumber     int             `json:"prNumber"`
	PrURL        string          `json:"prUrl"`
	Message      json.RawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`          // tool_use
	Name      string          `json:"name"`        // tool_use
	Input     json.RawMessage `json:"input"`       // tool_use
	ToolUseID string          `json:"tool_use_id"` // tool_result
	Content   json.RawMessage `json:"content"`     // tool_result (string | []block)
}

type toolInput struct {
	Command  string `json:"command"`
	FilePath string `json:"file_path"`
	Pattern  string `json:"pattern"`
}

// useInfo links a tool_use id to the tool that produced it, so a later
// tool_result can be attributed back to its tool and brief.
type useInfo struct{ name, brief string }

// Parse reads a transcript and builds a Session. When collectDetail is
// false the heavy per-call slices (Bash, Files, FatResults) are skipped —
// `sf cc ls` only needs the aggregate counters and usage totals.
func Parse(path string, collectDetail bool) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	s := &Session{
		Path:             path,
		ToolCalls:        map[string]int{},
		ToolResultTokens: map[string]int64{},
	}
	if info, err := f.Stat(); err == nil {
		s.SizeBytes = info.Size()
	}
	s.FullID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	s.ID = shortID(s.FullID)

	// id -> (tool name, brief) so tool_results can be attributed to the
	// tool_use that produced them.
	uses := map[string]useInfo{}
	files := map[string]*FileTouch{}
	prsSeen := map[int]bool{}

	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 {
			var e rawEntry
			if json.Unmarshal(line, &e) == nil {
				ingestEntry(s, &e, uses, files, prsSeen, collectDetail)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
	}

	if s.Project == "" {
		s.Project = projectFromDir(filepath.Base(filepath.Dir(path)))
	}
	if collectDetail {
		s.Files = sortFiles(files)
		sort.Slice(s.FatResults, func(i, j int) bool { return s.FatResults[i].Tokens > s.FatResults[j].Tokens })
		if len(s.FatResults) > fatResultTopN {
			s.FatResults = s.FatResults[:fatResultTopN]
		}
	}
	return s, nil
}

func ingestEntry(s *Session, e *rawEntry, uses map[string]useInfo, files map[string]*FileTouch, prsSeen map[int]bool, detail bool) {
	if e.Cwd != "" && s.Cwd == "" {
		s.Cwd = e.Cwd
		s.Project = filepath.Base(e.Cwd)
	}
	if e.GitBranch != "" {
		s.Branch = e.GitBranch
	}
	if e.Version != "" {
		s.Version = e.Version
	}
	if e.SessionID != "" && s.FullID == "" {
		s.FullID = e.SessionID
		s.ID = shortID(e.SessionID)
	}
	if e.AiTitle != "" {
		s.Title = e.AiTitle
	}
	if e.MessageCount > s.Messages {
		s.Messages = e.MessageCount
	}
	if e.DurationMs > 0 {
		s.DurationMs += e.DurationMs
	}
	if e.PrNumber > 0 && !prsSeen[e.PrNumber] {
		prsSeen[e.PrNumber] = true
		s.PRs = append(s.PRs, PR{Number: e.PrNumber, URL: e.PrURL})
	}
	if t := parseTime(e.Timestamp); !t.IsZero() {
		if s.Start.IsZero() || t.Before(s.Start) {
			s.Start = t
		}
		if t.After(s.End) {
			s.End = t
		}
	}

	if len(e.Message) == 0 {
		return
	}
	var m rawMessage
	if json.Unmarshal(e.Message, &m) != nil {
		return
	}
	if m.Model != "" {
		s.Model = m.Model
	}
	if m.Usage != nil {
		s.OutputTokens += m.Usage.OutputTokens
		s.InputTokens += m.Usage.InputTokens
		s.CacheReadTokens += m.Usage.CacheReadInputTokens
		s.CacheCreateTokens += m.Usage.CacheCreationInputTokens
	}

	switch e.Type {
	case "assistant":
		ingestAssistant(s, m, uses, files, detail)
	case "user":
		ingestUser(s, e, m, uses, detail)
	}
}

func ingestAssistant(s *Session, m rawMessage, uses map[string]useInfo, files map[string]*FileTouch, detail bool) {
	blocks := decodeBlocks(m.Content)
	for _, b := range blocks {
		if b.Type == "text" {
			if t := strings.TrimSpace(b.Text); t != "" {
				s.LastText = t // last non-empty assistant narrative wins
			}
			continue
		}
		if b.Type != "tool_use" {
			continue
		}
		s.ToolCalls[b.Name]++
		var in toolInput
		_ = json.Unmarshal(b.Input, &in)
		brief := toolBrief(b.Name, in)
		uses[b.ID] = useInfo{name: b.Name, brief: brief}
		if !detail {
			continue
		}
		switch b.Name {
		case "Bash":
			if in.Command != "" {
				s.Bash = append(s.Bash, in.Command)
			}
		case "Read", "Edit", "Write", "NotebookEdit":
			if in.FilePath != "" {
				ft := files[in.FilePath]
				if ft == nil {
					ft = &FileTouch{Path: in.FilePath}
					files[in.FilePath] = ft
				}
				switch b.Name {
				case "Read":
					ft.Reads++
				case "Edit":
					ft.Edits++
				case "Write", "NotebookEdit":
					ft.Writes++
				}
			}
		}
	}
}

func ingestUser(s *Session, e *rawEntry, m rawMessage, uses map[string]useInfo, detail bool) {
	// A user message is either a batch of tool_results or a human turn.
	// Try to decode content as an array of blocks first.
	if blocks := decodeBlocks(m.Content); blocks != nil {
		sawResult := false
		var texts []string
		for _, b := range blocks {
			switch b.Type {
			case "tool_result":
				sawResult = true
				info := uses[b.ToolUseID]
				tk := tokens.Estimate(resultText(b.Content))
				if info.name != "" {
					s.ToolResultTokens[info.name] += tk
				} else {
					s.ToolResultTokens["?"] += tk
				}
				if detail && tk >= fatResultMinTokens {
					name := info.name
					if name == "" {
						name = "?"
					}
					s.FatResults = append(s.FatResults, FatResult{Tokens: tk, Tool: name, Brief: info.brief})
				}
			case "text":
				texts = append(texts, b.Text)
			}
		}
		if !sawResult && !e.IsMeta {
			addPrompt(s, strings.Join(texts, "\n"))
		}
		return
	}
	// content was a bare string → a human prompt.
	if !e.IsMeta {
		var str string
		if json.Unmarshal(m.Content, &str) == nil {
			addPrompt(s, str)
		}
	}
}

// addPrompt stores the full, untruncated human turn. Display truncation
// and capping are a rendering concern (see `show`); `prompts` shows them
// verbatim.
func addPrompt(s *Session, text string) {
	if !isHumanText(text) || len(s.UserPrompts) >= maxPromptsStored {
		return
	}
	s.UserPrompts = append(s.UserPrompts, strings.TrimSpace(text))
}

// isHumanText filters out injected wrappers (system reminders, command
// stdout/name envelopes, interrupt notices) so only genuine human turns
// land in UserPrompts.
func isHumanText(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	if t[0] == '<' {
		return false
	}
	for _, p := range []string{"Caveat:", "[Request interrupted", "API Error", "This session is being continued"} {
		if strings.HasPrefix(t, p) {
			return false
		}
	}
	return true
}

func toolBrief(name string, in toolInput) string {
	switch name {
	case "Bash":
		return truncate(in.Command, 100)
	case "Read", "Edit", "Write", "NotebookEdit":
		return in.FilePath
	case "Grep", "Glob":
		return in.Pattern
	default:
		return name
	}
}

// decodeBlocks tries to decode content as an array of content blocks.
// Returns nil when content is not an array (e.g. a bare string).
func decodeBlocks(raw json.RawMessage) []contentBlock {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "[") {
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

// resultText flattens a tool_result content (string or []block) into the
// text the model actually saw, for token estimation.
func resultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	blocks := decodeBlocks(raw)
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func sortFiles(m map[string]*FileTouch) []FileTouch {
	out := make([]FileTouch, 0, len(m))
	for _, ft := range m {
		out = append(out, *ft)
	}
	sort.Slice(out, func(i, j int) bool {
		ti := out[i].Reads + out[i].Edits + out[i].Writes
		tj := out[j].Reads + out[j].Edits + out[j].Writes
		if ti != tj {
			return ti > tj
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func shortID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// projectFromDir derives a readable project label from an encoded project
// dir name like "-home-user-www-myapp" → "myapp".
func projectFromDir(dir string) string {
	dir = strings.TrimPrefix(dir, "-")
	if i := strings.LastIndexByte(dir, '-'); i >= 0 {
		return dir[i+1:]
	}
	return dir
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// relPath shortens p to a session-cwd-relative path when possible.
func relPath(cwd, p string) string {
	if cwd == "" {
		return p
	}
	if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}
