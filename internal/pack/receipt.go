package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sofia-ctx/sofia/internal/plugin"
)

// ReceiptVersion is the on-disk schema of a pack receipt. Bumped only on a
// breaking change to the receipt shape.
const ReceiptVersion = 1

// PacksDir is where installed packs' canon copies and receipts live:
// $XDG_DATA_HOME/sofia/packs, a sibling of plugin.PluginsDir under the same
// sofia data dir — reusing plugin.DataDir keeps one XDG root for the whole
// tool instead of inventing a second one.
func PacksDir() string { return filepath.Join(plugin.DataDir(), "packs") }

// receiptsDir holds one JSON file per installed pack (see receiptPath).
func receiptsDir() string { return filepath.Join(PacksDir(), ".receipts") }

// receiptPath is where a pack's receipt lives. The .receipts/ dot-dir can't
// collide with a pack's own canon copy: a pack name can never start with a
// dot (see nameRe).
func receiptPath(name string) string { return filepath.Join(receiptsDir(), name+".json") }

// canonDir is the canonical, on-disk copy of an installed pack's source tree
// — what `sf pack list`/`info` read, and what `sf pack list` itself
// enumerates.
func canonDir(name string) string { return filepath.Join(PacksDir(), name) }

// Source records where a pack came from: a git remote (URL/Ref/Commit) or a
// local directory (Path). Exactly one of URL or Path is set.
type Source struct {
	URL    string `json:"url,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
	Path   string `json:"path,omitempty"`
}

// ClaudeFile is one file the pack placed on the Claude shelf. Dest is
// absolute — the shelf lives outside any project — so Status/Uninstall can
// stat it directly.
type ClaudeFile struct {
	Dest   string `json:"dest"`
	SHA256 string `json:"sha256"`
}

// ProjectFile is one file the pack placed in a project. Dest is relative to
// the project root (the owning ProjectInstall's key in Receipt.Projects) and
// always "/"-separated on disk (Windows groundwork).
type ProjectFile struct {
	Dest   string `json:"dest"`
	SHA256 string `json:"sha256"`
}

// ProjectInstall is one project's slice of a pack install: when it happened
// and which files landed there.
type ProjectInstall struct {
	InstalledAt time.Time     `json:"installed_at"`
	Files       []ProjectFile `json:"files"`
}

// Receipt is what Install writes to receiptPath(name) and what
// Uninstall/Status read back — the one source of truth for "what did this
// pack actually put on disk", so drift detection never has to re-derive it
// from the manifest (which may have moved on since).
type Receipt struct {
	Version     int                       `json:"version"`
	Name        string                    `json:"name"`
	Source      Source                    `json:"source"`
	InstalledAt time.Time                 `json:"installed_at"`
	Plugins     []string                  `json:"plugins,omitempty"`
	Claude      []ClaudeFile              `json:"claude,omitempty"`
	Projects    map[string]ProjectInstall `json:"projects"`
}

// loadReceipt reads a pack's receipt. found is false when no receipt exists
// yet — a fresh, empty Receipt (with a non-nil Projects map, ready to
// receive an entry) is returned in that case rather than an error, so a first
// install can merge into it exactly like a reinstall would.
func loadReceipt(name string) (Receipt, bool, error) {
	data, err := os.ReadFile(receiptPath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return Receipt{Name: name, Projects: map[string]ProjectInstall{}}, false, nil
		}
		return Receipt{}, false, err
	}
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return Receipt{}, false, fmt.Errorf("%s: %w", receiptPath(name), err)
	}
	if r.Projects == nil {
		r.Projects = map[string]ProjectInstall{}
	}
	return r, true, nil
}

// saveReceipt writes r to receiptPath(r.Name), creating .receipts/ as needed.
func saveReceipt(r Receipt) error {
	if err := os.MkdirAll(receiptsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(receiptPath(r.Name), data, 0o644)
}

// deleteReceipt removes a pack's receipt file (the final step of a full
// uninstall, once no project references it any more). A missing file is not
// an error — it's already gone.
func deleteReceipt(name string) error {
	err := os.Remove(receiptPath(name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// sha256File hashes a file's contents, for comparing what's on disk against
// what a receipt recorded.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
