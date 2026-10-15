// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

// Package myextension is a Margince extension unit: one served agent tool,
// my_extension_ping, routed at GET /v1/ext/my-extension/ping.
package myextension

import (
	"context"
	"encoding/json"

	"github.com/margince/margince/backend/pkg/extension"
)

// New returns the unit's declaration. It holds no handle into the core; the
// runtime reaches the handler only when a call arrives.
func New() extension.Extension {
	return extension.Extension{
		Name:        "my-extension",
		Version:     "0.2.1",
		Description: "A sample unit that answers one ping, to prove it composes, boots and serves.",
		Tools: []extension.Tool{
			{Name: "my_extension_ping", Handle: ping},
		},
	}
}

func ping(_ context.Context, _ extension.Runtime, _ json.RawMessage) (json.RawMessage, error) {
	return json.Marshal(map[string]bool{"ok": true})
}
