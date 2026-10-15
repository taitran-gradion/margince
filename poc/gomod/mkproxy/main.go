// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

// mkproxy publishes one module folder at one version into a GOPROXY=file:// tree,
// standing in for a pushed tag fetched through proxy.golang.org.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/zip"
)

func main() {
	proxy := flag.String("proxy", "", "proxy root")
	path := flag.String("module", "", "module path")
	version := flag.String("version", "", "version, e.g. v0.1.0")
	dir := flag.String("dir", "", "module folder")
	flag.Parse()

	mv := module.Version{Path: *path, Version: *version}
	escaped, err := module.EscapePath(*path)
	if err != nil {
		log.Fatal(err)
	}
	at := filepath.Join(*proxy, escaped, "@v")
	if err := os.MkdirAll(at, 0o755); err != nil {
		log.Fatal(err)
	}
	zf, err := os.Create(filepath.Join(at, *version+".zip"))
	if err != nil {
		log.Fatal(err)
	}
	if err := zip.CreateFromDir(zf, mv, *dir); err != nil {
		log.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		log.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(*dir, "go.mod"))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(at, *version+".mod"), mod, 0o644); err != nil {
		log.Fatal(err)
	}
	info, _ := json.Marshal(map[string]any{"Version": *version, "Time": time.Now().UTC()})
	if err := os.WriteFile(filepath.Join(at, *version+".info"), info, 0o644); err != nil {
		log.Fatal(err)
	}
	listFile := filepath.Join(at, "list")
	raw, _ := os.ReadFile(listFile)
	versions := strings.Fields(string(raw))
	if !slices.Contains(versions, *version) {
		versions = append(versions, *version)
	}
	if err := os.WriteFile(listFile, []byte(strings.Join(versions, "\n")+"\n"), 0o644); err != nil {
		log.Fatal(err)
	}
}
