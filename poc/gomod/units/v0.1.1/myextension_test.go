// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package myextension

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewNamesTheUnitAfterItsFolder(t *testing.T) {
	if got := New().Name; got != "my-extension" {
		t.Fatalf("Name = %q, want my-extension: the composer refuses a name that is not the folder name", got)
	}
}

func TestPingAnswersOK(t *testing.T) {
	out, err := ping(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	var body struct{ OK bool }
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("ping returned %s, not JSON: %v", out, err)
	}
	if !body.OK {
		t.Fatalf("ping returned %s, want ok=true", out)
	}
}
