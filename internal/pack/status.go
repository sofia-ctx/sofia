package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PackStatus summarizes one installed pack's drift: how many of its recorded
// files still match what was installed, how many changed, how many vanished.
// Ok+Modified+Missing always add up to the receipt's total file count
// (claude files plus every project's files).
type PackStatus struct {
	Name     string
	Ok       int
	Modified int
	Missing  int
	Projects []string // project roots this pack is installed in, sorted
}

// Info gathers everything `sf pack info`/`list` show beyond drift: the
// receipt plus the pack's own description. Description isn't part of the
// receipt (see receipt.go) — it's read fresh from the canonical copy's
// pack.yaml, which is the source of truth for the pack's own metadata.
type Info struct {
	Receipt     Receipt
	Description string
}

// Status computes drift for one installed pack. A pack with no receipt is
// reported as an error — there is no drift to report for something that was
// never installed.
func Status(name string) (PackStatus, error) {
	r, found, err := loadReceipt(name)
	if err != nil {
		return PackStatus{}, err
	}
	if !found {
		return PackStatus{}, fmt.Errorf("no pack named %q (see `sf pack list`)", name)
	}
	return status(r), nil
}

// StatusAll computes drift for every installed pack, sorted by name.
func StatusAll() ([]PackStatus, error) {
	names, err := ListInstalled()
	if err != nil {
		return nil, err
	}
	out := make([]PackStatus, 0, len(names))
	for _, name := range names {
		r, _, err := loadReceipt(name)
		if err != nil {
			return nil, err
		}
		out = append(out, status(r))
	}
	return out, nil
}

// ListInstalled returns the names of every installed pack (one receipt per
// pack), sorted. This is what `sf pack list` enumerates.
func ListInstalled() ([]string, error) {
	entries, err := os.ReadDir(receiptsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// LoadInfo loads a pack's receipt plus its description, for `sf pack info`
// and `sf pack list`.
func LoadInfo(name string) (Info, error) {
	r, found, err := loadReceipt(name)
	if err != nil {
		return Info{}, err
	}
	if !found {
		return Info{}, fmt.Errorf("no pack named %q (see `sf pack list`)", name)
	}
	info := Info{Receipt: r}
	if data, err := os.ReadFile(filepath.Join(canonDir(name), manifestFile)); err == nil {
		if m, err := ParseManifest(data); err == nil {
			info.Description = m.Description
		}
	}
	return info, nil
}

// status is the sha-compare drift check shared by Status/StatusAll — mirrors
// doctor.checkSkill's byte-compare-against-the-repo pattern, but against the
// receipt's recorded sha256 instead of a second file to diff against.
func status(r Receipt) PackStatus {
	st := PackStatus{Name: r.Name}
	check := func(dest, wantSHA string) {
		sha, err := sha256File(dest)
		switch {
		case err != nil:
			st.Missing++
		case sha == wantSHA:
			st.Ok++
		default:
			st.Modified++
		}
	}
	for _, f := range r.Claude {
		check(f.Dest, f.SHA256)
	}
	projects := make([]string, 0, len(r.Projects))
	for proj := range r.Projects {
		projects = append(projects, proj)
	}
	sort.Strings(projects)
	st.Projects = projects
	for _, proj := range projects {
		for _, f := range r.Projects[proj].Files {
			check(filepath.Join(proj, filepath.FromSlash(f.Dest)), f.SHA256)
		}
	}
	return st
}
