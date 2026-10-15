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
// that disagrees stops the build.
func fetchRequiredUnits(root string) error {
	outDir := filepath.Join(root, filepath.FromSlash(fetchedUnitsDir))
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(root, extensionsModFile)) // #nosec G304 -- the installation's own file at a fixed path
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	mod, err := modfile.Parse(extensionsModFile, raw, nil)
	if err != nil {
		return err
	}
	if len(mod.Replace) > 0 {
		return fmt.Errorf("%s: replace is not composed yet — develop a local unit under extensions/ instead", extensionsModFile)
	}
	if len(mod.Require) == 0 {
		return nil
	}
	sums, err := readSums(filepath.Join(root, extensionsSumFile))
	if err != nil {
		return err
	}
	downloaded, err := downloadModules(mod.Require)
	if err != nil {
		return err
	}
	seen := map[string]string{}
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
	return writeSums(filepath.Join(root, extensionsSumFile), sums)
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
