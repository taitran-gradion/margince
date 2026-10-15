// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

const (
	extensionsModFile = "extensions.mod"
	extensionsSumFile = "extensions.sum"
	// fetchedUnitsDir holds every unit extensions.mod requires, copied out of the
	// read-only module cache. It is rebuilt on each run, so a require removed from
	// extensions.mod leaves no folder behind that would still turn its unit on.
	fetchedUnitsDir = "build/extensions"
	// localUnitsFile lists the units extensions.mod replaces with a local
	// folder, one "<name>\t<absolute dir>" per line.
	localUnitsFile = "build/extensions.local"
)

var majorVersionSuffix = regexp.MustCompile(`^v[0-9]+$`)

// downloadedModule is the part of `go mod download -json` this composer reads.
type downloadedModule struct {
	Path     string
	Version  string
	Dir      string
	Sum      string
	GoModSum string
	Error    string
}

// fetchRequiredUnits materializes every unit extensions.mod requires under
// build/extensions/<name>/, after checking each download against extensions.sum.
// A sum the file lacks is added, the way `go get` adds one to go.sum; a sum
// that disagrees stops the build. A unit replaced by a local folder is used in
// place, listed in build/extensions.local, so its author's edits are what runs.
func fetchRequiredUnits(root, modPath string) error {
	outDir := filepath.Join(root, filepath.FromSlash(fetchedUnitsDir))
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	localList := filepath.Join(root, filepath.FromSlash(localUnitsFile))
	if err := os.Remove(localList); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if modPath == "" {
		modPath = filepath.Join(root, extensionsModFile)
	}
	raw, err := os.ReadFile(modPath) // #nosec G304 -- the installation's own file, named by the operator
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	mod, err := modfile.Parse(modPath, raw, nil)
	if err != nil {
		return err
	}
	local, remote, err := splitLocalUnits(mod, filepath.Dir(modPath))
	if err != nil {
		return err
	}
	if err := writeLocalUnits(localList, local); err != nil {
		return err
	}
	if len(remote) == 0 {
		return nil
	}
	sumPath := filepath.Join(filepath.Dir(modPath), extensionsSumFile)
	sums, err := readSums(sumPath)
	if err != nil {
		return err
	}
	downloaded, err := downloadModules(remote)
	if err != nil {
		return err
	}
	seen := map[string]string{}
	for _, l := range local {
		seen[l.name] = l.dir
	}
	for _, d := range downloaded {
		name := unitNameFromModulePath(d.Path)
		if other, dup := seen[name]; dup {
			return fmt.Errorf("%s: %s and %s both resolve to unit name %q", extensionsModFile, other, d.Path, name)
		}
		seen[name] = d.Path
		if err := checkSum(sums, d.Path+" "+d.Version, d.Sum); err != nil {
			return err
		}
		if err := checkSum(sums, d.Path+" "+d.Version+"/go.mod", d.GoModSum); err != nil {
			return err
		}
		if err := copyUnit(d.Dir, filepath.Join(outDir, name)); err != nil {
			return fmt.Errorf("%s@%s: %w", d.Path, d.Version, err)
		}
	}
	return writeSums(sumPath, sums)
}

// localUnit is a required unit an extensions.mod replace points at a folder.
type localUnit struct {
	name string
	dir  string
}

// splitLocalUnits separates the requires a replace points at a local folder
// from the ones to download. Only a folder replace is composed: a replace onto
// another module version would make the sum file record one module and the
// build compile another.
func splitLocalUnits(mod *modfile.File, base string) ([]localUnit, []*modfile.Require, error) {
	required := map[string]bool{}
	for _, r := range mod.Require {
		required[r.Mod.Path] = true
	}
	replaced := map[string]string{}
	for _, r := range mod.Replace {
		if r.New.Version != "" {
			return nil, nil, fmt.Errorf("%s: replace %s => %s %s: only a local folder can replace a unit", extensionsModFile, r.Old.Path, r.New.Path, r.New.Version)
		}
		if !required[r.Old.Path] {
			return nil, nil, fmt.Errorf("%s: replace %s names a module it does not require", extensionsModFile, r.Old.Path)
		}
		dir := r.New.Path
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		replaced[r.Old.Path] = filepath.Clean(dir)
	}
	var local []localUnit
	var remote []*modfile.Require
	for _, r := range mod.Require {
		dir, ok := replaced[r.Mod.Path]
		if !ok {
			remote = append(remote, r)
			continue
		}
		if err := requireModulePath(dir, r.Mod.Path); err != nil {
			return nil, nil, err
		}
		local = append(local, localUnit{name: unitNameFromModulePath(r.Mod.Path), dir: dir})
	}
	return local, remote, nil
}

// requireModulePath refuses a folder whose go.mod declares some other module:
// the replace would compose a unit under a name its own code does not carry.
func requireModulePath(dir, want string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "go.mod")) // #nosec G304 -- a folder the installation's own extensions.mod names
	if err != nil {
		return fmt.Errorf("%s: replace %s => %s: %w", extensionsModFile, want, dir, err)
	}
	if got := modfile.ModulePath(raw); got != want {
		return fmt.Errorf("%s: replace %s => %s: that folder is module %q", extensionsModFile, want, dir, got)
	}
	return nil
}

func writeLocalUnits(file string, local []localUnit) error {
	if len(local) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil { // #nosec G301 -- build output stays readable to another UID
		return err
	}
	var b strings.Builder
	for _, l := range local {
		fmt.Fprintf(&b, "%s\t%s\n", l.name, l.dir)
	}
	return os.WriteFile(file, []byte(b.String()), 0o644) // #nosec G306 -- build output
}

// readLocalUnits reads what the last fetch listed, so -verify judges the same
// set the generate run composed.
func readLocalUnits(root string) ([]localUnit, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(localUnitsFile))) // #nosec G304 -- build output at a fixed path
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var local []localUnit
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		name, dir, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("%s: malformed line %q — run make composition again", localUnitsFile, line)
		}
		local = append(local, localUnit{name: name, dir: dir})
	}
	return local, nil
}

// downloadModules asks the go command for each required module, outside any
// workspace, so the answer is the module cache's verified copy and not a
// workspace member that happens to share the path.
func downloadModules(reqs []*modfile.Require) ([]downloadedModule, error) {
	args := []string{"mod", "download", "-json"}
	for _, r := range reqs {
		args = append(args, r.Mod.Path+"@"+r.Mod.Version)
	}
	cmd := exec.Command("go", args...) // #nosec G204 -- module paths and versions parsed from extensions.mod by modfile
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	cmd.Dir = os.TempDir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	dec := json.NewDecoder(bytes.NewReader(out))
	var mods []downloadedModule
	for dec.More() {
		var m downloadedModule
		if derr := dec.Decode(&m); derr != nil {
			return nil, derr
		}
		if m.Error != "" {
			return nil, fmt.Errorf("%s: %s", extensionsModFile, m.Error)
		}
		mods = append(mods, m)
	}
	if err != nil {
		return nil, fmt.Errorf("go mod download: %w: %s", err, stderr.String())
	}
	return mods, nil
}

// unitNameFromModulePath takes the unit name from the module path's last
// element, skipping a major-version suffix (…/crm/v2 is the unit crm).
func unitNameFromModulePath(modulePath string) string {
	base := path.Base(modulePath)
	if majorVersionSuffix.MatchString(base) {
		return path.Base(path.Dir(modulePath))
	}
	return base
}

func readSums(file string) (map[string]string, error) {
	sums := map[string]string{}
	raw, err := os.ReadFile(file) // #nosec G304 -- the installation's own file at a fixed path
	if errors.Is(err, fs.ErrNotExist) {
		return sums, nil
	}
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s:%d: want <module> <version> <hash>", extensionsSumFile, i+1)
		}
		sums[fields[0]+" "+fields[1]] = fields[2]
	}
	return sums, nil
}

func checkSum(sums map[string]string, key, got string) error {
	want, known := sums[key]
	if !known {
		sums[key] = got
		return nil
	}
	if want != got {
		return fmt.Errorf("%s: %s downloaded as %s, recorded as %s — the published module changed; do not edit the sum to match, find out why", extensionsSumFile, key, got, want)
	}
	return nil
}

func writeSums(file string, sums map[string]string) error {
	keys := make([]string, 0, len(sums))
	for k := range sums {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %s\n", k, sums[k])
	}
	return os.WriteFile(file, []byte(b.String()), 0o644) // #nosec G306 -- a checksum file meant to be committed
}

// copyUnit copies a module out of the read-only cache into a writable folder:
// the frontend install and the manifest check both write beside the unit.
func copyUnit(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755) // #nosec G301 -- build output stays readable to another UID
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		content, err := os.ReadFile(p) // #nosec G304 -- a file inside a verified module zip's extraction
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644) // #nosec G306 -- build output
	})
}
