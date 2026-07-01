// Package cliflags holds tiny, dependency-free Cobra flag helpers shared
// by every tool in the repo (format aliases, etc.). Keeping these in a
// separate package avoids import cycles with internal/cli (which composes
// the master CLI tree from the concrete tool commands).
package cliflags

import (
	"fmt"

	"github.com/spf13/cobra"
)

// AttachFormatFlags adds --format (default "toon") plus --md and --json
// boolean aliases to cmd. Call this from any tool that emits TOON/MD/JSON
// to keep the flag surface consistent. The resolved format is written to
// *format via a PreRunE that runs before the command's own RunE.
func AttachFormatFlags(cmd *cobra.Command, format *string) {
	cmd.Flags().StringVar(format, "format", "toon", "output format: toon | md | json")
	cmd.Flags().Bool("md", false, "alias for --format=md")
	cmd.Flags().Bool("json", false, "alias for --format=json")
	_ = cmd.RegisterFlagCompletionFunc("format", FormatCompletion)
	chainPreRunE(cmd, func(c *cobra.Command, _ []string) error {
		md, _ := c.Flags().GetBool("md")
		j, _ := c.Flags().GetBool("json")
		if md && j {
			return fmt.Errorf("--md and --json are mutually exclusive")
		}
		fmtChanged := c.Flags().Changed("format")
		if md {
			if fmtChanged && *format != "md" {
				return fmt.Errorf("--md conflicts with --format=%s", *format)
			}
			*format = "md"
		}
		if j {
			if fmtChanged && *format != "json" {
				return fmt.Errorf("--json conflicts with --format=%s", *format)
			}
			*format = "json"
		}
		return nil
	})
}

// FormatCompletion suggests known output formats with short descriptions.
// Bash and fish render the trailing "\tdescription" as the help column.
func FormatCompletion(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return []string{
		"toon\tToken-Oriented Object Notation (default, smallest token cost)",
		"md\tMarkdown",
		"json\tJSON",
	}, cobra.ShellCompDirectiveNoFileComp
}

// DirOnly is a ShellCompDirective shortcut for flags that take a directory.
// Returning it from RegisterFlagCompletionFunc tells the shell to complete
// directory names only (no regular files).
func DirOnly(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveFilterDirs
}

func chainPreRunE(cmd *cobra.Command, next func(c *cobra.Command, args []string) error) {
	prev := cmd.PreRunE
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		if prev != nil {
			if err := prev(c, args); err != nil {
				return err
			}
		}
		return next(c, args)
	}
}
