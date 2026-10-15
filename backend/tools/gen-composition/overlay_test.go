// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const coreDealsSource = `package deals

type Store struct{}

// CreateDeal is core's.
func (s *Store) CreateDeal(name string) string { return "core:" + name }

func helper() int { return 1 }
`

const overlaySource = overlayBuildTag + `

package deals

//margince:decorate
func (s *Store) CreateDeal(name string) string { return "acme:" + s.CreateDeal__core(name) }
`

// overlayTree is a core with one package and a local unit decorating it.
func overlayTree(t *testing.T) (string, extensionUnit) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "backend/internal/modules/deals/deal.go"), coreDealsSource)
	dir := filepath.Join(root, "addons", "acme")
	writeFile(t, filepath.Join(dir, overlaysLayer, "internal/modules/deals/acme.go"), overlaySource)
	return root, extensionUnit{Name: "acme", Dir: dir}
}

func lockCreateDeal(t *testing.T, root string, u extensionUnit) {
	t.Helper()
	_, src, err := findCoreFunc(filepath.Join(root, "backend"), "internal/modules/deals", "(*Store).CreateDeal")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(u.Dir, overlaysLockFile), "internal/modules/deals.(*Store).CreateDeal "+bytesSHA256(src)+"\n")
}

func TestAnOverlayRenamesOnlyTheFunctionItDecorates(t *testing.T) {
	root, u := overlayTree(t)
	lockCreateDeal(t, root, u)
	if err := composeOverlays(root, []extensionUnit{u}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, composedOverlayDir, "overlay.json"))
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct{ Replace map[string]string }
	if err := json.Unmarshal(raw, &overlay); err != nil {
		t.Fatal(err)
	}
	core := overlay.Replace[filepath.Join(root, "backend/internal/modules/deals/deal.go")]
	rewritten, err := os.ReadFile(core)
	if err != nil {
		t.Fatalf("core file not in the overlay: %v", err)
	}
	if !strings.Contains(string(rewritten), "func (s *Store) CreateDeal__core(") || !strings.Contains(string(rewritten), "func helper() int") {
		t.Fatalf("rewritten core file:\n%s\nwant CreateDeal renamed and every other line kept", rewritten)
	}
	if len(overlay.Replace) != 2 {
		t.Fatalf("overlay = %v, want the rewritten core file and the unit's file", overlay.Replace)
	}
}

func TestOverlayRefusals(t *testing.T) {
	cases := map[string]struct {
		edit func(t *testing.T, root string, u extensionUnit) []extensionUnit
		want string
	}{
		"no lock line": {
			edit: func(_ *testing.T, _ string, u extensionUnit) []extensionUnit { return []extensionUnit{u} },
			want: "names no hash",
		},
		"core changed the function": {
			edit: func(t *testing.T, root string, u extensionUnit) []extensionUnit {
				lockCreateDeal(t, root, u)
				writeFile(t, filepath.Join(root, "backend/internal/modules/deals/deal.go"), strings.Replace(coreDealsSource, `"core:"`, `"core2:"`, 1))
				return []extensionUnit{u}
			},
			want: "changed since the overlay was written",
		},
		"a fetched unit": {
			edit: func(t *testing.T, root string, u extensionUnit) []extensionUnit {
				fetched := filepath.Join(root, fetchedUnitsDir, "acme")
				writeFile(t, filepath.Join(fetched, overlaysLayer, "internal/modules/deals/acme.go"), overlaySource)
				return []extensionUnit{{Name: "acme", Dir: fetched}}
			},
			want: "only an installation's own folder",
		},
		"no build tag": {
			edit: func(t *testing.T, root string, u extensionUnit) []extensionUnit {
				lockCreateDeal(t, root, u)
				writeFile(t, filepath.Join(u.Dir, overlaysLayer, "internal/modules/deals/acme.go"), strings.TrimPrefix(overlaySource, overlayBuildTag))
				return []extensionUnit{u}
			},
			want: "must start with",
		},
		"two decorators of one function": {
			edit: func(t *testing.T, root string, u extensionUnit) []extensionUnit {
				lockCreateDeal(t, root, u)
				other := extensionUnit{Name: "beta", Dir: filepath.Join(root, "addons", "beta")}
				writeFile(t, filepath.Join(other.Dir, overlaysLayer, "internal/modules/deals/beta.go"), overlaySource)
				lockCreateDeal(t, root, other)
				return []extensionUnit{u, other}
			},
			want: "both decorate internal/modules/deals.(*Store).CreateDeal",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root, u := overlayTree(t)
			err := composeOverlays(root, tc.edit(t, root, u))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
