package phpmove

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
)

type Receipt struct {
	Transaction string `json:"transaction"`
	Files       int    `json:"files"`
	Status      string `json:"status"`
}

// Apply re-plans the original intent and rejects any difference before touching
// project files. A journal and original bytes remain under .sf-move. Each file
// replacement is atomic, but the multi-file move is not. Keep other writers out
// of the workspace while applying. Warnings require explicit acknowledgement.
func Apply(plan *Plan, acceptWarnings bool) (*Receipt, error) {
	return apply(plan, acceptWarnings, nil)
}

// checkpoint is used only by package tests to inject I/O failures after writes.
func apply(plan *Plan, acceptWarnings bool, checkpoint func(int) error) (*Receipt, error) {
	if plan == nil || plan.Version != PlanVersion {
		return nil, fmt.Errorf("invalid plan version")
	}
	if len(plan.Warnings) > 0 && !acceptWarnings {
		return nil, fmt.Errorf("plan has %d review warnings; inspect them before using --accept-warnings", len(plan.Warnings))
	}
	r, err := os.OpenRoot(plan.Root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := noSymlinks(r, ".sf-move/lock"); err != nil {
		return nil, err
	}
	if err := r.Mkdir(".sf-move", 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	lock, err := r.OpenFile(".sf-move/lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot acquire move lock (check an existing transaction before removing a stale lock): %w", err)
	}
	defer lock.Close()
	keepLock := false
	defer func() {
		if !keepLock {
			_ = r.Remove(".sf-move/lock")
		}
	}()
	fresh, err := build(r, plan.Root, plan.From, plan.To, plan.Skip)
	if err != nil {
		return nil, fmt.Errorf("revalidate plan: %w", err)
	}
	if !reflect.DeepEqual(plan, fresh) {
		return nil, fmt.Errorf("plan is stale or modified; build and review a new plan")
	}
	if err := validateFilesystem(r, plan); err != nil {
		return nil, err
	}
	encoded, err := Encode(plan)
	if err != nil {
		return nil, err
	}
	id := ".sf-move/txn-" + rand.Text()
	if err := r.Mkdir(id, 0700); err != nil {
		return nil, err
	}
	if _, err := lock.WriteString(id + "\n"); err != nil {
		return nil, err
	}
	if err := lock.Sync(); err != nil {
		return nil, err
	}
	if err := writeExclusive(r, id+"/plan.json", encoded, 0600); err != nil {
		return nil, err
	}
	for i, c := range plan.Changes {
		if err := writeExclusive(r, fmt.Sprintf("%s/before-%d", id, i), c.Before, fs.FileMode(c.Mode)); err != nil {
			return nil, err
		}
		if err := writeExclusive(r, fmt.Sprintf("%s/after-%d", id, i), c.After, fs.FileMode(c.Mode)); err != nil {
			return nil, err
		}
	}
	t := &transaction{root: r, plan: plan, id: id, checkpoint: checkpoint}
	if err := t.status("prepared"); err != nil {
		return nil, err
	}
	if err := t.run(); err != nil {
		rollbackErr := t.rollback()
		if rollbackErr != nil {
			keepLock = true
			_ = t.status("rollback_failed")
			return nil, fmt.Errorf("move failed: %w; rollback incomplete: %v; journal %s/%s (lock retained)", err, rollbackErr, plan.Root, id)
		}
		if stateErr := t.status("rolled_back"); stateErr != nil {
			return nil, fmt.Errorf("move failed and source restored, but recording rollback failed: %v; original error: %w; journal %s/%s", stateErr, err, plan.Root, id)
		}
		return nil, fmt.Errorf("move failed and was rolled back: %w; journal %s/%s", err, plan.Root, id)
	}
	if err := t.status("applied"); err != nil {
		keepLock = true
		return nil, fmt.Errorf("files moved, but recording completion failed: %w; journal %s/%s (lock retained)", err, plan.Root, id)
	}
	return &Receipt{Transaction: path.Join(plan.Root, id), Files: len(plan.Changes), Status: "applied"}, nil
}

type operation struct {
	kind  string
	index int
	name  string
	mode  fs.FileMode
}
type transaction struct {
	root       *os.Root
	plan       *Plan
	id         string
	ops        []operation
	checkpoint func(int) error
}

func (t *transaction) status(value string) error {
	// Each state is its own exclusive durable marker. Previous states survive a
	// failed write, and plan.json always contains recovery bytes.
	return writeExclusive(t.root, t.id+"/"+value, []byte(value+"\n"), 0600)
}
func (t *transaction) record(op operation) error {
	t.ops = append(t.ops, op)
	if t.checkpoint != nil {
		return t.checkpoint(len(t.ops))
	}
	return nil
}
func (t *transaction) directory(name string, mode fs.FileMode) error {
	if name == "." {
		return nil
	}
	if err := noSymlinks(t.root, name); err != nil {
		return err
	}
	if info, err := t.root.Lstat(name); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("not a directory: %s", name)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := t.directory(path.Dir(name), 0755); err != nil {
		return err
	}
	if err := t.root.Mkdir(name, mode); err != nil {
		return err
	}
	if err := t.record(operation{kind: "mkdir", name: name}); err != nil {
		return err
	}
	return t.root.Chmod(name, mode)
}
func (t *transaction) run() error {
	for _, d := range t.plan.Directories {
		if err := t.directory(d.To, fs.FileMode(d.Mode)); err != nil {
			return err
		}
	}
	for i, c := range t.plan.Changes {
		if err := noSymlinks(t.root, c.From); err != nil {
			return err
		}
		if err := matches(t.root, c.From, c.Before, c.Mode); err != nil {
			return err
		}
		if err := t.directory(path.Dir(c.To), 0755); err != nil {
			return err
		}
		if err := noSymlinks(t.root, c.To); err != nil {
			return err
		}
		stage := fmt.Sprintf("%s/after-%d", t.id, i)
		if c.From != c.To {
			if err := t.root.Link(stage, c.To); err != nil {
				return err
			}
			if err := t.record(operation{kind: "create", index: i}); err != nil {
				return err
			}
		} else {
			if err := t.root.Rename(stage, c.To); err != nil {
				return err
			}
			if err := t.record(operation{kind: "replace", index: i}); err != nil {
				return err
			}
		}
	}
	for i, c := range t.plan.Changes {
		if c.From == c.To {
			continue
		}
		if err := noSymlinks(t.root, c.From); err != nil {
			return err
		}
		if err := matches(t.root, c.From, c.Before, c.Mode); err != nil {
			return err
		}
		if err := t.root.Remove(c.From); err != nil {
			return err
		}
		if err := t.record(operation{kind: "remove", index: i}); err != nil {
			return err
		}
	}
	dirs := append([]Directory(nil), t.plan.Directories...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].From) > len(dirs[j].From) })
	for _, d := range dirs {
		if err := noSymlinks(t.root, d.From); err != nil {
			return err
		}
		if err := t.root.Remove(d.From); err != nil {
			return err
		}
		if err := t.record(operation{kind: "rmdir", name: d.From, mode: fs.FileMode(d.Mode)}); err != nil {
			return err
		}
	}
	return nil
}

func (t *transaction) rollback() error {
	var failures []error
	for i := len(t.ops) - 1; i >= 0; i-- {
		op := t.ops[i]
		if err := t.undo(op); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (t *transaction) undo(op operation) error {
	if op.kind == "mkdir" {
		return t.root.Remove(op.name)
	}
	if op.kind == "rmdir" {
		if err := noSymlinks(t.root, op.name); err != nil {
			return err
		}
		if err := t.root.Mkdir(op.name, op.mode); err != nil {
			return err
		}
		return t.root.Chmod(op.name, op.mode)
	}
	c := t.plan.Changes[op.index]
	if err := noSymlinks(t.root, c.To); err != nil {
		return err
	}
	if err := noSymlinks(t.root, c.From); err != nil {
		return err
	}
	backup := fmt.Sprintf("%s/before-%d", t.id, op.index)
	switch op.kind {
	case "remove":
		return t.root.Link(backup, c.From)
	case "create":
		if err := matches(t.root, c.To, c.After, c.Mode); err != nil {
			return err
		}
		return t.root.Remove(c.To)
	case "replace":
		if err := matches(t.root, c.To, c.After, c.Mode); err != nil {
			return err
		}
		restore := fmt.Sprintf("%s/restore-%d", t.id, op.index)
		if err := t.root.Link(backup, restore); err != nil {
			return err
		}
		return t.root.Rename(restore, c.To)
	}
	return fmt.Errorf("unknown rollback operation %s", op.kind)
}
func matches(r *os.Root, name string, data []byte, mode uint32) error {
	info, err := r.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != mode {
		return fmt.Errorf("file type or permissions changed: %s", name)
	}
	current, err := readBounded(r, name)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, current) {
		return fmt.Errorf("file contents changed: %s", name)
	}
	return nil
}
func writeExclusive(r *os.Root, name string, data []byte, mode fs.FileMode) error {
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
