package launch

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// searchAndPick deep-searches for a project named `name`, resolves it to a
// single dir — prompting to disambiguate when several match — and caches the
// winner as an alias so the next launch is instant. found is false when
// nothing matched, so the caller can surface the original not-found error
// instead. The listing/prompt go to out; a pick is read from stdin.
func searchAndPick(name string, out io.Writer) (dir string, found bool, err error) {
	cands := SearchProjects(name)
	switch len(cands) {
	case 0:
		return "", false, nil
	case 1:
		dir = cands[0]
	default:
		if dir, err = promptPick(name, cands, out); err != nil {
			return "", true, err
		}
	}
	if e := SaveAlias(name, dir); e != nil {
		fmt.Fprintf(os.Stderr, "note: couldn't save alias %q: %v\n", name, e)
	} else {
		fmt.Fprintf(os.Stderr, "saved alias %s -> %s\n", name, dir)
	}
	return dir, true, nil
}

// promptPick lists the candidates and reads a 1-based choice from stdin.
// pickFrom defaults to os.Stdin; tests inject a reader. An immediate EOF means
// stdin is non-interactive (a script, --task, closed input), so it returns an
// error telling the user to pick with --dir rather than hanging; an empty line
// from a real terminal is a cancel.
var pickFrom io.Reader = os.Stdin

func promptPick(name string, cands []string, out io.Writer) (string, error) {
	fmt.Fprintf(out, "multiple projects named %q:\n", name)
	for i, c := range cands {
		fmt.Fprintf(out, "  %d) %s\n", i+1, c)
	}
	fmt.Fprintf(out, "pick [1-%d] (q to cancel): ", len(cands))
	sc := bufio.NewScanner(pickFrom)
	if !sc.Scan() {
		return "", fmt.Errorf("ambiguous project %q (%d matches); re-run with --dir <path>", name, len(cands))
	}
	switch choice := strings.TrimSpace(sc.Text()); choice {
	case "", "q", "Q":
		return "", fmt.Errorf("cancelled")
	default:
		n, err := strconv.Atoi(choice)
		if err != nil || n < 1 || n > len(cands) {
			return "", fmt.Errorf("invalid selection %q", choice)
		}
		return cands[n-1], nil
	}
}
