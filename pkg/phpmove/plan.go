// Package phpmove plans and applies bounded, offline Composer PSR-4 moves.
// Plans contain source bytes and must be stored with the same care as the code.
package phpmove

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sofia-ctx/sofia/pkg/php"
)

const (
	PlanVersion   = 1
	MaxFiles      = 50000
	MaxFileBytes  = 8 << 20
	MaxTotalBytes = 256 << 20
	MaxPlanBytes  = 128 << 20
)

type Snapshot struct {
	Path string `json:"path"`
	Hash string `json:"sha256"`
	Mode uint32 `json:"mode"`
}

type Change struct {
	From   string           `json:"from"`
	To     string           `json:"to"`
	Mode   uint32           `json:"mode"`
	Before []byte           `json:"before"`
	After  []byte           `json:"after"`
	Edits  []php.SourceEdit `json:"edits,omitempty"`
}

type Directory struct {
	From string `json:"from"`
	To   string `json:"to"`
	Mode uint32 `json:"mode"`
}

type Warning struct {
	Path   string `json:"path"`
	Line   int    `json:"line,omitempty"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type Plan struct {
	Version     int               `json:"version"`
	Root        string            `json:"root"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	Skip        []string          `json:"skip"`
	Classes     map[string]string `json:"classes"`
	Changes     []Change          `json:"changes"`
	Directories []Directory       `json:"directories"`
	Inputs      []Snapshot        `json:"inputs"`
	Excluded    []string          `json:"excluded"`
	Warnings    []Warning         `json:"warnings"`
}

type mapping struct{ prefix, directory string }
type source struct {
	path string
	mode fs.FileMode
	data []byte
}

// Build reads the project but never creates files. To is the exact destination,
// which must not exist. It is not an existing directory to move From into.
func Build(root, from, to string) (*Plan, error) {
	return BuildExcluding(root, from, to, nil)
}

// BuildExcluding explicitly omits additional relative files or directory trees.
// Exclusions are retained in the reviewed plan and cannot overlap the move.
func BuildExcluding(root, from, to string, skip []string) (*Plan, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return build(r, abs, from, to, skip)
}

func build(r *os.Root, root, from, to string, skip []string) (*Plan, error) {
	if err := validatePath(from); err != nil {
		return nil, err
	}
	if err := validatePath(to); err != nil {
		return nil, err
	}
	if from == "composer.json" || to == "composer.json" {
		return nil, fmt.Errorf("cannot relocate the project Composer manifest")
	}
	if from == to || within(to, from) || within(from, to) {
		return nil, fmt.Errorf("source and destination must not overlap")
	}
	for _, omitted := range skip {
		if err := validatePath(omitted); err != nil {
			return nil, err
		}
		if omitted == "composer.json" || omitted == from || omitted == to || within(from, omitted) || within(to, omitted) || within(omitted, from) || within(omitted, to) {
			return nil, fmt.Errorf("exclusion overlaps move or manifest: %s", omitted)
		}
	}
	for _, p := range []string{from, to, "composer.json"} {
		if err := noSymlinks(r, p); err != nil {
			return nil, err
		}
	}
	info, err := r.Lstat(from)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("unsupported source type: %s", from)
	}
	if _, err := r.Lstat(to); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("destination exists: %s", to)
	}
	composer, err := readBounded(r, "composer.json")
	if err != nil {
		return nil, err
	}
	mappings, err := composerMappings(composer)
	if err != nil {
		return nil, err
	}
	p := &Plan{Version: PlanVersion, Root: root, From: from, To: to, Classes: map[string]string{}}
	p.Skip = append([]string(nil), skip...)
	sort.Strings(p.Skip)
	var files []source
	var oversized []string
	var total, count int
	err = fs.WalkDir(r.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		moved := name == from || info.IsDir() && within(name, from)
		if excludedComponent(entry.Name()) || explicitlySkipped(name, p.Skip) {
			if moved {
				return fmt.Errorf("source contains excluded path: %s", name)
			}
			if entry.Name() != ".sf-move" {
				p.Excluded = append(p.Excluded, name)
			}
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		count++
		if count > MaxFiles {
			return fmt.Errorf("scan exceeds %d entries", MaxFiles)
		}
		stat, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if moved {
				return fmt.Errorf("symlink in source: %s", name)
			}
			target, err := r.Readlink(name)
			if err != nil {
				return err
			}
			p.Inputs = append(p.Inputs, Snapshot{name, digest([]byte(target)), uint32(stat.Mode())})
			p.Warnings = append(p.Warnings, Warning{Path: name, Kind: "symlink", Detail: "Not followed or searched"})
			return nil
		}
		if entry.IsDir() {
			if moved {
				p.Directories = append(p.Directories, Directory{name, to + strings.TrimPrefix(name, from), uint32(stat.Mode().Perm())})
			}
			return nil
		}
		if !stat.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", name)
		}
		if !moved && !searchable(name) {
			return nil
		}
		if stat.Size() > MaxFileBytes {
			oversized = append(oversized, fmt.Sprintf("%s (%d bytes)", name, stat.Size()))
			return nil
		}
		if stat.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
			return fmt.Errorf("special permissions are unsupported: %s", name)
		}
		data, err := readBounded(r, name)
		if err != nil {
			return err
		}
		total += len(data)
		if total > MaxTotalBytes {
			return fmt.Errorf("scan exceeds %d bytes", MaxTotalBytes)
		}
		p.Inputs = append(p.Inputs, Snapshot{name, digest(data), uint32(stat.Mode().Perm())})
		files = append(files, source{name, stat.Mode().Perm(), data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(oversized) > 0 {
		return nil, fmt.Errorf("files exceed the %d-byte scan limit; explicitly exclude irrelevant files or reduce scope before planning:\n%s", MaxFileBytes, strings.Join(oversized, "\n"))
	}
	// Map every moved declaration before resolving consumers, so directory peers
	// can continue using their existing local names in the destination namespace.
	for _, f := range files {
		if !p.moved(f.path) || !isPHP(f.path) {
			continue
		}
		old, err := classForPath(f.path, mappings)
		if err != nil {
			return nil, err
		}
		newName, err := classForPath(p.destination(f.path), mappings)
		if err != nil {
			return nil, err
		}
		for existing, target := range p.Classes {
			if strings.EqualFold(existing, old) || strings.EqualFold(target, newName) {
				return nil, fmt.Errorf("ambiguous moved classes: %s", old)
			}
		}
		p.Classes[old] = newName
	}
	for _, f := range files {
		moved := p.moved(f.path)
		after := f.data
		var edits []php.SourceEdit
		if isPHP(f.path) && (moved || candidate(f.data, p.Classes)) {
			options := php.Relocation{Classes: p.Classes, Move: moved}
			var expected string
			if moved {
				expected, _ = classForPath(f.path, mappings)
				options.Namespace = namespace(expected)
				options.NewNamespace = namespace(p.Classes[expected])
			}
			result, err := php.Relocate(string(f.data), f.path, options)
			if err != nil {
				return nil, err
			}
			if moved && (len(result.Declarations) != 1 || result.Declarations[0] != expected) {
				return nil, fmt.Errorf("%s: declaration must match PSR-4 name %s", f.path, expected)
			}
			if !moved {
				for _, declaration := range result.Declarations {
					for _, target := range p.Classes {
						if strings.EqualFold(declaration, target) {
							return nil, fmt.Errorf("destination class already declared in %s: %s", f.path, declaration)
						}
					}
				}
			}
			after, edits = []byte(result.Source), result.Edits
			for _, w := range result.Warnings {
				p.Warnings = append(p.Warnings, Warning{f.path, w.Line, w.Kind, w.Detail})
			}
		}
		if !isPHP(f.path) && possibleReference(f.data, from, p.Classes) {
			p.Warnings = append(p.Warnings, Warning{Path: f.path, Kind: "text_reference", Detail: "Possible class or path reference outside PHP syntax; content is unchanged"})
		}
		if isPHP(f.path) && bytes.Contains(bytes.ToLower(f.data), []byte(strings.ToLower(from))) {
			p.Warnings = append(p.Warnings, Warning{Path: f.path, Kind: "path_reference", Detail: "Possible literal source path; review after relocation"})
		}
		if moved || !bytes.Equal(f.data, after) {
			p.Changes = append(p.Changes, Change{From: f.path, To: p.destination(f.path), Mode: uint32(f.mode), Before: f.data, After: after, Edits: edits})
		}
	}
	sort.Slice(p.Warnings, func(i, j int) bool {
		a, b := p.Warnings[i], p.Warnings[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Detail < b.Detail
	})
	return p, nil
}

func (p *Plan) moved(name string) bool { return name == p.From || within(name, p.From) }
func (p *Plan) destination(name string) string {
	if p.moved(name) {
		return p.To + strings.TrimPrefix(name, p.From)
	}
	return name
}
func within(name, parent string) bool { return strings.HasPrefix(name, parent+"/") }
func digest(data []byte) string       { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func namespace(fqcn string) string {
	i := strings.LastIndex(fqcn, `\`)
	if i < 0 {
		return ""
	}
	return fqcn[:i]
}
func isPHP(name string) bool { return strings.EqualFold(path.Ext(name), ".php") }
func searchable(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".php", ".inc", ".phtml", ".json", ".yaml", ".yml", ".xml", ".neon", ".ini", ".twig", ".md", ".ts", ".tsx", ".js", ".jsx", ".vue", ".txt":
		return true
	}
	return false
}
func excludedComponent(name string) bool {
	switch name {
	case ".git", ".sf-move", ".idea", "vendor", "node_modules", "var":
		return true
	}
	return false
}

func explicitlySkipped(name string, skip []string) bool {
	for _, omitted := range skip {
		if name == omitted || within(name, omitted) {
			return true
		}
	}
	return false
}
func validatePath(name string) error {
	if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00\r\n\t") || strings.Contains(name, ":") {
		return fmt.Errorf("expected clean project-relative path: %q", name)
	}
	for _, component := range strings.Split(name, "/") {
		if excludedComponent(component) {
			return fmt.Errorf("excluded path: %s", name)
		}
	}
	return nil
}
func noSymlinks(r *os.Root, name string) error {
	parts := strings.Split(name, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		info, err := r.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed: %s", p)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("parent is not a directory: %s", p)
		}
	}
	return nil
}
func readBounded(r *os.Root, name string) ([]byte, error) {
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, MaxFileBytes)
	}
	return data, nil
}
func candidate(data []byte, classes map[string]string) bool {
	lower := bytes.ToLower(data)
	for old, next := range classes {
		for _, name := range []string{old, next} {
			i := strings.LastIndex(name, `\`)
			if bytes.Contains(lower, []byte(strings.ToLower(name[i+1:]))) {
				return true
			}
		}
	}
	return false
}
func possibleReference(data []byte, from string, classes map[string]string) bool {
	lower := strings.ToLower(string(data))
	if strings.Contains(lower, strings.ToLower(from)) {
		return true
	}
	for name := range classes {
		name = strings.ToLower(name)
		if strings.Contains(lower, name) || strings.Contains(lower, strings.ReplaceAll(name, `\`, `\\`)) {
			return true
		}
	}
	return false
}

func composerMappings(data []byte) ([]mapping, error) {
	type section struct {
		PSR4 map[string]json.RawMessage `json:"psr-4"`
	}
	var doc struct {
		Autoload section `json:"autoload"`
		Dev      section `json:"autoload-dev"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("composer.json: %w", err)
	}
	var result []mapping
	for _, section := range []section{doc.Autoload, doc.Dev} {
		for prefix, raw := range section.PSR4 {
			if prefix != "" && !strings.HasSuffix(prefix, `\`) {
				return nil, fmt.Errorf("PSR-4 namespace prefix must end in a backslash: %s", prefix)
			}
			var dirs []string
			var single string
			if err := json.Unmarshal(raw, &single); err == nil {
				dirs = []string{single}
			} else if err := json.Unmarshal(raw, &dirs); err != nil {
				return nil, fmt.Errorf("invalid PSR-4 mapping for %s", prefix)
			}
			for _, directory := range dirs {
				directory = strings.TrimSuffix(strings.TrimPrefix(directory, "./"), "/")
				if directory == "" {
					directory = "."
				}
				if directory != "." {
					if err := validatePath(directory); err != nil {
						return nil, fmt.Errorf("PSR-4: %w", err)
					}
				}
				result = append(result, mapping{prefix, directory})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].directory != result[j].directory {
			return result[i].directory < result[j].directory
		}
		return result[i].prefix < result[j].prefix
	})
	return result, nil
}
func classForPath(name string, mappings []mapping) (string, error) {
	if path.Ext(name) != ".php" {
		return "", fmt.Errorf("PHP destination must end in .php: %s", name)
	}
	best := -1
	var match string
	for _, m := range mappings {
		if m.directory != "." && !within(name, m.directory) {
			continue
		}
		relative := name
		if m.directory != "." {
			relative = strings.TrimPrefix(name, m.directory+"/")
		}
		fqcn := m.prefix + strings.ReplaceAll(strings.TrimSuffix(relative, path.Ext(relative)), "/", `\`)
		score := len(m.directory)
		if score < best {
			continue
		}
		if score == best && match != fqcn {
			return "", fmt.Errorf("ambiguous PSR-4 mapping for %s", name)
		}
		best, match = score, fqcn
	}
	if match == "" || namespace(match) == "" {
		return "", fmt.Errorf("no named PSR-4 namespace for %s", name)
	}
	for _, part := range strings.Split(match, `\`) {
		if part == "" {
			return "", fmt.Errorf("invalid PSR-4 class name %s", match)
		}
		for i, b := range []byte(part) {
			if !(b == '_' || b >= 128 || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || i > 0 && b >= '0' && b <= '9') {
				return "", fmt.Errorf("invalid PSR-4 class name %s", match)
			}
		}
	}
	return match, nil
}

func Encode(p *Plan) ([]byte, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data)+1 > MaxPlanBytes {
		return nil, fmt.Errorf("plan exceeds %d bytes", MaxPlanBytes)
	}
	return append(data, '\n'), nil
}

func Decode(reader io.Reader) (*Plan, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxPlanBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxPlanBytes {
		return nil, fmt.Errorf("plan exceeds %d bytes", MaxPlanBytes)
	}
	var p Plan
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing plan data")
	}
	if p.Version != PlanVersion {
		return nil, fmt.Errorf("unsupported plan version %d", p.Version)
	}
	return &p, nil
}
