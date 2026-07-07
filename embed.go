// Package sofia holds assets embedded straight into the sf binary so a
// Homebrew (or `go install`) build — no repo checkout on disk — can still
// self-install them. Currently just the sf-context skill; see
// internal/common/initcmd (writes it to $CLAUDE_DIR) and doctor.checkSkill
// (falls back to comparing against it when repoRoot() can't be found).
package sofia

import _ "embed"

//go:embed skills/sf-context/SKILL.md
var SkillMD []byte
