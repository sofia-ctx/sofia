package hook

import (
	"encoding/json"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sofia-ctx/sofia/internal/calllog"
)

// output is the PreToolUse response envelope Claude Code expects.
type output struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
}

type hookSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// NewCommand returns the hidden `sf hook` group. It is harness plumbing
// (wired in ~/.claude/settings.json), not a tool for humans, hence Hidden.
func NewCommand() *cobra.Command {
	root := &cobra.Command{
		Use:    "hook",
		Short:  "Claude Code hook endpoints (plumbing for settings.json)",
		Hidden: true,
	}
	pre := &cobra.Command{
		Use:   "pre",
		Short: "PreToolUse: nudge full Read/cat of big source files toward `sf code`",
		Long: `Reads the PreToolUse JSON payload from stdin and answers with a permission
decision. Big source files (.go/.php/.ts/.tsx/.vue, ≥ SOFIA_HOOK_MIN_BYTES,
default 8192) read in full via Read or bare cat get nudged toward the
structural path. Modes via SOFIA_HOOK_MODE: off | suggest | nudge (default,
deny-once-then-allow per session+file) | strict.

Configured globally:

  ~/.claude/settings.json → hooks.PreToolUse:
    {"matcher": "Read|Bash",
     "hooks": [{"type": "command", "command": "sf hook pre", "timeout": 10}]}

Fail-open: on any internal problem the call is allowed silently. Only fired
nudges are logged (tool "hook.nudge"); the pass-through path writes nothing.`,
		Args: cobra.NoArgs,
		RunE: runPre,
	}
	root.AddCommand(pre)
	return root
}

func runPre(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
	if err != nil {
		return nil // fail-open
	}
	var in Input
	if json.Unmarshal(raw, &in) != nil {
		return nil
	}
	mode := Mode()
	st := NewState(filepath.Join(filepath.Dir(calllog.Path()), "hook"))
	d := Decide(in, st, mode, MinBytes())
	if d.Action == "" {
		return nil
	}

	// Self-log the fired nudge (the silent pass-through must not spam the
	// log — calllog skips the bare "hook" tool, like gripe's list view).
	t := calllog.Start("hook.nudge", []string{in.ToolName, d.Path})
	t.SetSummary(map[string]any{
		"action": d.Action, "mode": mode, "tool": in.ToolName,
		"file": d.Path, "bytes": d.Bytes,
	})
	cw := &calllog.Counter{W: cmd.OutOrStdout()}
	resp := output{hookSpecific{HookEventName: "PreToolUse"}}
	switch d.Action {
	case ActionSuggest:
		resp.HookSpecificOutput.PermissionDecision = "allow"
		resp.HookSpecificOutput.AdditionalContext = d.Reason
	case ActionDeny:
		resp.HookSpecificOutput.PermissionDecision = "deny"
		resp.HookSpecificOutput.PermissionDecisionReason = d.Reason
	}
	enc := json.NewEncoder(cw)
	_ = enc.Encode(resp)
	t.RecordOutput(cw)
	t.Finish(nil)
	return nil
}
