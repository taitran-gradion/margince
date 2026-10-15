// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package main

import (
	"os"
	"path/filepath"
)

// backendLayer is the folder a unit may keep its Go package in, beside api/
// and frontend/, instead of the module root.
const backendLayer = "backend"

// unitGoDir is the unit's backend/ when it holds Go, else the module root.
func unitGoDir(dir string) (string, error) {
	backend := filepath.Join(dir, backendLayer)
	if info, err := os.Stat(backend); err != nil || !info.IsDir() {
		return dir, nil
	}
	hasGo, err := hasRootGoFiles(backend)
	if err != nil || !hasGo {
		return dir, err
	}
	return backend, nil
}

// goDir is the folder holding the unit's Go package.
func (u extensionUnit) goDir() string {
	if u.GoDir == "" {
		return u.Dir
	}
	return u.GoDir
}

// importPath is the package the composition imports for the unit.
func (u extensionUnit) importPath() string {
	if u.GoImport == "" {
		return u.ModulePath
	}
	return u.GoImport
}

func goImportPath(modulePath, dir, goDir string) string {
	if goDir == dir {
		return modulePath
	}
	return modulePath + "/" + backendLayer
}
