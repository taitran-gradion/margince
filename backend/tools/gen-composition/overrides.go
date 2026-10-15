// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// overridesLayer mirrors core's frontend/ tree: a unit file at
	// frontend/overrides/src/x.tsx stands in for core's frontend/src/x.tsx.
	overridesLayer = "frontend/overrides"
	// overridesLockFile pins each override to the core file it was written
	// against, by hash, so a core change to that file stops the build.
	overridesLockFile = "frontend/overrides.lock"
	// overridableRoot keeps overrides to UI code; the API client, auth and the
	// generated composition stay core's alone.
	overridableRoot = "src/screens/"
	// composedOverridesFile tells the bundler which unit file replaces which
	// core file. composedOverridesCheck holds each override to the exports of
	// the file it replaces, under tsc.
	composedOverridesFile  = "build/composition-frontend/overrides.json"
	composedOverridesCheck = "build/composition-frontend/overrides.check.ts"
)

// fileOverride is one core frontend file a unit replaces.
type fileOverride struct {
	Unit   string `json:"unit"`
	Target string `json:"target"` // relative to core's frontend/
	Source string `json:"source"` // absolute
}

// composeOverrides writes what the bundler and tsc need to apply every unit's
// overrides, after collectOverrides accepted them.
func composeOverrides(root string, units []extensionUnit) error {
	overrides, err := collectOverrides(root, units)
	if err != nil {
		return err
	}
	return emitOverrides(root, overrides)
}

// collectOverrides refuses an override the build cannot stand behind. Its
// target may be missing from core, outside overridableRoot or claimed twice.
// Or core's file may have changed since the unit's lock was written.
func collectOverrides(root string, units []extensionUnit) ([]fileOverride, error) {
	frontendDir := filepath.Join(root, "frontend")
	owner := map[string]string{}
	var all []fileOverride
	for _, u := range units {
		found, err := unitOverrides(u, frontendDir)
		if err != nil {
			return nil, err
		}
		for _, o := range found {
			if other, taken := owner[o.Target]; taken {
				return nil, fmt.Errorf("%s and %s both override frontend/%s — one file has one owner", other, u.Name, o.Target)
			}
			owner[o.Target] = u.Name
			all = append(all, o)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Target < all[j].Target })
	return all, nil
}

func unitOverrides(u extensionUnit, frontendDir string) ([]fileOverride, error) {
	layer := filepath.Join(u.Dir, filepath.FromSlash(overridesLayer))
	if _, err := os.Stat(layer); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	lock, err := readOverridesLock(filepath.Join(u.Dir, filepath.FromSlash(overridesLockFile)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u.Name, err)
	}
	var found []fileOverride
	err = filepath.WalkDir(layer, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(layer, p)
		if err != nil {
			return err
		}
		target := filepath.ToSlash(rel)
		if !strings.HasPrefix(target, overridableRoot) {
			return fmt.Errorf("%s: %s/%s: only files under frontend/%s can be overridden", u.Name, overridesLayer, target, overridableRoot)
		}
		got, err := fileSHA256(filepath.Join(frontendDir, filepath.FromSlash(target)))
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s: %s/%s overrides frontend/%s, which this core does not have", u.Name, overridesLayer, target, target)
		}
		if err != nil {
			return err
		}
		want, locked := lock[target]
		delete(lock, target)
		switch {
		case !locked:
			return fmt.Errorf("%s: %s names no hash for %s — after reading core's file, add the line:\n  %s %s", u.Name, overridesLockFile, target, target, got)
		case want != got:
			return fmt.Errorf("%s: core's frontend/%s changed since the override was written (%s, now %s) — re-read it, update %s/%s, then set the lock line to:\n  %s %s",
				u.Name, target, want, got, overridesLayer, target, target, got)
		}
		found = append(found, fileOverride{Unit: u.Name, Target: target, Source: p})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for stale := range lock {
		return nil, fmt.Errorf("%s: %s pins %s, but %s/ has no such file", u.Name, overridesLockFile, stale, overridesLayer)
	}
	return found, nil
}

// readOverridesLock reads "<target> sha256:<hex>" lines; # starts a comment.
func readOverridesLock(file string) (map[string]string, error) {
	f, err := os.Open(file) // #nosec G304 -- a unit's own lock at a fixed path
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() //craft:ignore swallowed-errors a read-only file's close reports nothing actionable
	lock := map[string]string{}
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text, _, _ := strings.Cut(sc.Text(), "#")
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want <path> sha256:<hex>", overridesLockFile, line)
		}
		lock[fields[0]] = fields[1]
	}
	return lock, sc.Err()
}

func fileSHA256(file string) (string, error) {
	raw, err := os.ReadFile(file) // #nosec G304 -- a core frontend file an override names
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// emitOverrides writes the bundler's map and the type check, replacing both so
// an override removed from its unit leaves nothing that would still apply it.
func emitOverrides(root string, overrides []fileOverride) error {
	mapFile := filepath.Join(root, filepath.FromSlash(composedOverridesFile))
	checkFile := filepath.Join(root, filepath.FromSlash(composedOverridesCheck))
	if err := os.MkdirAll(filepath.Dir(mapFile), 0o755); err != nil { // #nosec G301 -- build output stays readable to another UID
		return err
	}
	encoded, err := json.MarshalIndent(overrides, "", "  ")
	if err != nil {
		return err
	}
	if overrides == nil {
		encoded = []byte("[]")
	}
	if err := os.WriteFile(mapFile, append(encoded, '\n'), 0o644); err != nil { // #nosec G306 -- build output
		return err
	}
	var b strings.Builder
	b.WriteString("// GENERATED by gen-composition. Each override must keep every export of the\n")
	b.WriteString("// core file it replaces, with a type core's callers still accept.\n")
	b.WriteString("type Keeps<_Override extends Original, Original> = true;\n")
	for i, o := range overrides {
		fmt.Fprintf(&b, "import type * as Original%d from %q;\n", i, "@core-original/"+strings.TrimSuffix(o.Target, filepath.Ext(o.Target)))
		fmt.Fprintf(&b, "import type * as Override%d from %q;\n", i, strings.TrimSuffix(o.Source, filepath.Ext(o.Source)))
		fmt.Fprintf(&b, "export type Check%d = Keeps<typeof Override%d, typeof Original%d>;\n", i, i, i)
	}
	return os.WriteFile(checkFile, []byte(b.String()), 0o644) // #nosec G306 -- build output
}
