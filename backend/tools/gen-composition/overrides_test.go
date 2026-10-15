// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const coreHeader = "export function Header() { return null; }\n"

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// overrideTree is a core with one screen file and a unit that overrides it,
// locked with lockLine.
func overrideTree(t *testing.T, unit, lockLine string) (string, extensionUnit) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "frontend/src/screens/header.tsx"), coreHeader)
	dir := filepath.Join(root, "units", unit)
	writeFile(t, filepath.Join(dir, overridesLayer, "src/screens/header.tsx"), "export { Header } from \"x\";\n")
	writeFile(t, filepath.Join(dir, overridesLockFile), lockLine+"\n")
	return root, extensionUnit{Name: unit, Dir: dir}
}

func coreHeaderHash(t *testing.T, root string) string {
	t.Helper()
	sum, err := fileSHA256(filepath.Join(root, "frontend/src/screens/header.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestOverrideLockedToTheCurrentCoreFileIsComposed(t *testing.T) {
	root, u := overrideTree(t, "acme", "")
	writeFile(t, filepath.Join(u.Dir, overridesLockFile), "src/screens/header.tsx "+coreHeaderHash(t, root)+"\n")

	got, err := collectOverrides(root, []extensionUnit{u})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Target != "src/screens/header.tsx" || got[0].Unit != "acme" {
		t.Fatalf("got %+v, want acme overriding src/screens/header.tsx", got)
	}
}

func TestOverrideRefusals(t *testing.T) {
	cases := map[string]struct {
		edit func(t *testing.T, root string, u extensionUnit) []extensionUnit
		want string
	}{
		"no lock line": {
			edit: func(_ *testing.T, _ string, u extensionUnit) []extensionUnit { return []extensionUnit{u} },
			want: "names no hash",
		},
		"core file changed since the lock": {
			edit: func(t *testing.T, root string, u extensionUnit) []extensionUnit {
				writeFile(t, filepath.Join(u.Dir, overridesLockFile), "src/screens/header.tsx "+coreHeaderHash(t, root)+"\n")
				writeFile(t, filepath.Join(root, "frontend/src/screens/header.tsx"), coreHeader+"// changed\n")
				return []extensionUnit{u}
			},
			want: "changed since the override was written",
		},
		"core has no such file": {
			edit: func(t *testing.T, _ string, u extensionUnit) []extensionUnit {
				writeFile(t, filepath.Join(u.Dir, overridesLayer, "src/screens/gone.tsx"), "")
				return []extensionUnit{u}
			},
			want: "which this core does not have",
		},
		"outside screens": {
			edit: func(t *testing.T, _ string, u extensionUnit) []extensionUnit {
				writeFile(t, filepath.Join(u.Dir, overridesLayer, "src/api/client.ts"), "")
				return []extensionUnit{u}
			},
			want: "only files under frontend/src/screens/",
		},
		"two units, one file": {
			edit: func(t *testing.T, root string, u extensionUnit) []extensionUnit {
				line := "src/screens/header.tsx " + coreHeaderHash(t, root) + "\n"
				writeFile(t, filepath.Join(u.Dir, overridesLockFile), line)
				other := extensionUnit{Name: "beta", Dir: filepath.Join(root, "units", "beta")}
				writeFile(t, filepath.Join(other.Dir, overridesLayer, "src/screens/header.tsx"), "")
				writeFile(t, filepath.Join(other.Dir, overridesLockFile), line)
				return []extensionUnit{u, other}
			},
			want: "both override frontend/src/screens/header.tsx",
		},
		"lock pins a file the unit does not override": {
			edit: func(t *testing.T, root string, u extensionUnit) []extensionUnit {
				hash := coreHeaderHash(t, root)
				writeFile(t, filepath.Join(u.Dir, overridesLockFile), "src/screens/header.tsx "+hash+"\nsrc/screens/old.tsx "+hash+"\n")
				return []extensionUnit{u}
			},
			want: "has no such file",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root, u := overrideTree(t, "acme", "")
			_, err := collectOverrides(root, tc.edit(t, root, u))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestUnitAddsAScreenToCoresCustomFolderWithoutALock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "units", "acme")
	writeFile(t, filepath.Join(dir, overridesLayer, "src/screens/custom/acme/screen.tsx"), "export const screen = {};\n")
	writeFile(t, filepath.Join(dir, overridesLayer, "src/screens/custom/acme/panel.tsx"), "export {};\n")

	got, err := collectOverrides(root, []extensionUnit{{Name: "acme", Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Mount || got[0].Target != "src/screens/custom/acme/screen.tsx" {
		t.Fatalf("got %+v, want only screen.tsx mounted; panel.tsx is reached by its import", got)
	}
}

func TestAFileCoreLacksOutsideCustomIsStillRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "units", "acme")
	writeFile(t, filepath.Join(dir, overridesLayer, "src/screens/acme/screen.tsx"), "")

	_, err := collectOverrides(root, []extensionUnit{{Name: "acme", Dir: dir}})
	if err == nil || !strings.Contains(err.Error(), "which this core does not have") {
		t.Fatalf("err = %v, want a refusal naming the missing core file", err)
	}
}
