// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const backendUnitSource = "package acme\n\nfunc New() {}\n"

func backendUnit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.test/ext/acme\n\ngo 1.27\n")
	writeFile(t, filepath.Join(dir, backendLayer, "acme.go"), backendUnitSource)
	return dir
}

func TestAUnitMayKeepItsGoInBackend(t *testing.T) {
	dir := backendUnit(t)
	u, err := scanUnit("acme", dir)
	if err != nil {
		t.Fatal(err)
	}
	if u.GoDir != filepath.Join(dir, backendLayer) || u.importPath() != "example.test/ext/acme/backend" {
		t.Fatalf("GoDir %q import %q, want backend/ and its package path", u.GoDir, u.importPath())
	}
}

func TestAUnitWithGoInBothPlacesIsRefused(t *testing.T) {
	dir := backendUnit(t)
	writeFile(t, filepath.Join(dir, "root.go"), backendUnitSource)
	if _, err := scanUnit("acme", dir); err == nil || !strings.Contains(err.Error(), "one package, in one of the two") {
		t.Fatalf("err = %v, want a refusal of Go in both places", err)
	}
}

func TestASubpackageBesideBackendIsStillRefused(t *testing.T) {
	dir := backendUnit(t)
	writeFile(t, filepath.Join(dir, backendLayer, "internal", "live.go"), "package live\n")
	if _, err := scanUnit("acme", dir); err == nil || !strings.Contains(err.Error(), "outside the unit root") {
		t.Fatalf("err = %v, want the subpackage refused", err)
	}
}
