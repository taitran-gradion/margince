// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const (
	// overlaysLayer mirrors core's backend module, beside the unit's Go
	// package: backend/overlays/internal/modules/deals/x.go is compiled into
	// core's package deals.
	overlaysLayer = "overlays"
	// overlaysLockFile pins each decorated core function by the hash of its
	// source, so a core change to that function stops the build.
	overlaysLockFile = "overlays.lock"
	// overlayBuildTag keeps an overlay file out of the unit's own build: it
	// compiles only inside core's package, where its types are.
	overlayBuildTag = "//go:build margince_overlay"
	// decorateDirective marks a function that replaces core's function of the
	// same name; core's stays callable as <name>__core.
	decorateDirective = "//margince:decorate"
	coreSuffix        = "__core"
	// composedOverlayDir holds the rewritten core files, the units' files and
	// overlay.json, the map `go build -overlay` reads.
	composedOverlayDir = "build/overlay"
)

// composeReplacements writes everything units compile over core: frontend
// file overrides and backend function overlays.
func composeReplacements(root string, units []extensionUnit) error {
	if err := composeOverrides(root, units); err != nil {
		return err
	}
	return composeOverlays(root, units)
}

// declaredOverlays lists the "<package>.<function>" core functions a unit's
// overlays/ decorates, for its manifest.
func declaredOverlays(goDir string) ([]string, error) {
	layer := filepath.Join(goDir, overlaysLayer)
	var out []string
	err := filepath.WalkDir(layer, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipAll
		}
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(layer, filepath.Dir(p))
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Doc != nil && hasDirective(fn.Doc) {
				out = append(out, filepath.ToSlash(rel)+"."+funcKey(fn))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// decoration is one core function a unit's overlay replaces.
type decoration struct {
	unit    string
	pkg     string // relative to the backend module, slash-separated
	funcKey string // "(*Store).CreateDeal" or "Name"
}

// composeOverlays writes build/overlay for every unit's overlays/, or removes
// it when none has any, so a dropped overlay stops applying.
func composeOverlays(root string, units []extensionUnit) error {
	out := filepath.Join(root, filepath.FromSlash(composedOverlayDir))
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	// This tree's backend: Go refuses to overlay a file in the module cache,
	// so a build with overlays compiles core from a tree like this one.
	backend := filepath.Join(root, "backend")
	replace := map[string]string{}
	owners := map[string]string{}
	var decorations []decoration
	for _, u := range units {
		found, err := composeUnitOverlays(u, root, backend, out, replace, owners)
		if err != nil {
			return err
		}
		decorations = append(decorations, found...)
	}
	if len(replace) == 0 {
		return nil
	}
	if err := rewriteDecoratedCore(decorations, backend, out, replace); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(map[string]map[string]string{"Replace": replace}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "overlay.json"), append(encoded, '\n'), 0o644) // #nosec G306 -- build output
}

// composeUnitOverlays adds one unit's files to the overlay and returns the
// core functions they decorate, each checked against the unit's lock.
func composeUnitOverlays(u extensionUnit, root, backend, out string, replace, owners map[string]string) ([]decoration, error) {
	layer := filepath.Join(u.goDir(), overlaysLayer)
	if _, err := os.Stat(layer); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if fetched, err := filepath.Rel(filepath.Join(root, filepath.FromSlash(fetchedUnitsDir)), u.Dir); err == nil && !strings.HasPrefix(fetched, "..") {
		return nil, fmt.Errorf("%s: %s/ compiles code into core's own packages, which only an installation's own folder (a replace in extensions.mod) may do", u.Name, overlaysLayer)
	}
	lock, err := readOverridesLock(filepath.Join(u.goDir(), overlaysLockFile))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u.Name, err)
	}
	var found []decoration
	err = filepath.WalkDir(layer, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		rel, err := filepath.Rel(layer, filepath.Dir(p))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)
		decorated, err := unitOverlayFile(u, p, pkg, backend, out, replace)
		if err != nil {
			return err
		}
		for _, key := range decorated {
			d := decoration{unit: u.Name, pkg: pkg, funcKey: key}
			id := pkg + "." + key
			if other, taken := owners[id]; taken {
				return fmt.Errorf("%s and %s both decorate %s — one function has one decorator", other, u.Name, id)
			}
			owners[id] = u.Name
			if err := checkFunctionLock(lock, backend, d); err != nil {
				return fmt.Errorf("%s: %w", u.Name, err)
			}
			delete(lock, id)
			found = append(found, d)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for stale := range lock {
		return nil, fmt.Errorf("%s: %s pins %s, which no overlay decorates", u.Name, overlaysLockFile, stale)
	}
	return found, nil
}

// unitOverlayFile copies one overlay file, without its build tag, to where
// the build reads it, and returns the functions it marks as decorating core.
func unitOverlayFile(u extensionUnit, path, pkg, backend, out string, replace map[string]string) ([]string, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- a unit's own overlay file
	if err != nil {
		return nil, err
	}
	first, rest, _ := bytes.Cut(raw, []byte("\n"))
	if strings.TrimSpace(string(first)) != overlayBuildTag {
		return nil, fmt.Errorf("%s: %s must start with %q, so the unit's own build leaves it out", u.Name, path, overlayBuildTag)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, raw, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var decorated []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Doc != nil && hasDirective(fn.Doc) {
			decorated = append(decorated, funcKey(fn))
		}
	}
	target := filepath.Join(backend, filepath.FromSlash(pkg), "zz_overlay_"+strings.ReplaceAll(u.Name, "-", "_")+"_"+filepath.Base(path))
	copied := filepath.Join(out, "units", u.Name, filepath.FromSlash(pkg), filepath.Base(path))
	if err := writeBuildFile(copied, rest); err != nil {
		return nil, err
	}
	replace[target] = copied
	return decorated, nil
}

func hasDirective(doc *ast.CommentGroup) bool {
	for _, c := range doc.List {
		if strings.TrimSpace(c.Text) == decorateDirective {
			return true
		}
	}
	return false
}

// funcKey names a function the way a lock line and an error do.
func funcKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	switch recv := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := recv.X.(*ast.Ident); ok {
			return "(*" + id.Name + ")." + fn.Name.Name
		}
	case *ast.Ident:
		return "(" + recv.Name + ")." + fn.Name.Name
	}
	return fn.Name.Name
}

// checkFunctionLock compares a decorated function's source with the hash the
// unit's lock pins, and names the line to write when they disagree.
func checkFunctionLock(lock map[string]string, backend string, d decoration) error {
	_, src, err := findCoreFunc(backend, d.pkg, d.funcKey)
	if err != nil {
		return err
	}
	got := bytesSHA256(src)
	id := d.pkg + "." + d.funcKey
	want, locked := lock[id]
	switch {
	case !locked:
		return fmt.Errorf("%s names no hash for %s — after reading core's function, add the line:\n  %s %s", overlaysLockFile, id, id, got)
	case want != got:
		return fmt.Errorf("core's %s changed since the overlay was written (%s, now %s) — re-read it, update the overlay, then set the lock line to:\n  %s %s", id, want, got, id, got)
	}
	return nil
}

// findCoreFunc finds a function in a core package and returns its file and
// its source, from `func` to its closing brace.
func findCoreFunc(backend, pkg, key string) (string, []byte, error) {
	dir := filepath.Join(backend, filepath.FromSlash(pkg))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, fmt.Errorf("an overlay names core package %s, which this core does not have", pkg)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path) // #nosec G304 -- a core source file
		if err != nil {
			return "", nil, err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			return "", nil, err
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && funcKey(fn) == key {
				return path, raw[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset], nil
			}
		}
	}
	return "", nil, fmt.Errorf("an overlay decorates %s.%s, which this core does not have", pkg, key)
}

// rewriteDecoratedCore renames each decorated function to <name>__core in a
// copy of its file. Core's original stays callable, and every other line of
// the file compiles from core's own source.
func rewriteDecoratedCore(decorations []decoration, backend, out string, replace map[string]string) error {
	byFile := map[string][]string{}
	for _, d := range decorations {
		path, _, err := findCoreFunc(backend, d.pkg, d.funcKey)
		if err != nil {
			return err
		}
		byFile[path] = append(byFile[path], d.funcKey)
	}
	paths := make([]string, 0, len(byFile))
	for p := range byFile {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && slices.Contains(byFile[path], funcKey(fn)) {
				fn.Name.Name += coreSuffix
			}
		}
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, file); err != nil {
			return err
		}
		rel, err := filepath.Rel(backend, path)
		if err != nil {
			return err
		}
		copied := filepath.Join(out, "core", rel)
		if err := writeBuildFile(copied, buf.Bytes()); err != nil {
			return err
		}
		replace[path] = copied
	}
	return nil
}

func writeBuildFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- build output stays readable to another UID
		return err
	}
	return os.WriteFile(path, content, 0o644) // #nosec G306 -- build output
}
