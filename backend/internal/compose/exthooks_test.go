// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package compose

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/margince/margince/backend/internal/platform/database/storekit"
	"github.com/margince/margince/backend/internal/shared/kernel/ids"
	"github.com/margince/margince/backend/pkg/extension"
)

func withComposed(t *testing.T, exts ...extension.Extension) {
	t.Helper()
	before := ComposedExtensions()
	setComposedExtensions(exts)
	t.Cleanup(func() { setComposedExtensions(before) })
}

func TestAWriteHookRunsForItsEntityAndActionOnly(t *testing.T) {
	var seen []string
	withComposed(t, extension.Extension{Name: "acme", WriteHooks: []extension.WriteHook{{
		Entity: "deal", Actions: []string{"create"},
		Handle: func(_ context.Context, _ extension.Runtime, w extension.Write) error {
			seen = append(seen, w.Entity+"."+w.Action)
			return nil
		},
	}}})
	for _, w := range []storekit.AuditedWrite{
		{EntityType: "deal", Action: "create", EntityID: ids.NewV7()},
		{EntityType: "deal", Action: "update", EntityID: ids.NewV7()},
		{EntityType: "contact", Action: "create", EntityID: ids.NewV7()},
		{EntityType: "ext_acme_note", Action: "create", EntityID: ids.NewV7()},
	} {
		if err := dispatchWriteHooks(context.Background(), nil, w); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(seen, ",") != "deal.create" {
		t.Fatalf("hook saw %v, want only deal.create", seen)
	}
}

func TestAWriteHookRefusalRollsTheWriteBackAsAUnitRefusal(t *testing.T) {
	withComposed(t, extension.Extension{Name: "acme", WriteHooks: []extension.WriteHook{{
		Entity: "deal", Actions: []string{"create"},
		Handle: func(context.Context, extension.Runtime, extension.Write) error {
			return extension.ErrForbidden
		},
	}}})
	err := dispatchWriteHooks(context.Background(), nil, storekit.AuditedWrite{EntityType: "deal", Action: "create"})
	if err == nil {
		t.Fatal("a refusing hook let the write through")
	}
}

// hookedCreateDeal serves POST /v1/deals through operationHooks and reports
// the body the handler received.
func hookedCreateDeal(t *testing.T, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var received string
	r := chi.NewRouter()
	r.With(operationHooks).Post("/v1/deals", func(_ http.ResponseWriter, req *http.Request) {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		received = string(raw)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/deals", strings.NewReader(body)))
	return rec, received
}

func TestAnOperationHookChangesTheBodyCoreReads(t *testing.T) {
	withComposed(t, extension.Extension{Name: "acme", OperationHooks: []extension.OperationHook{{
		Operations: []string{"createDeal"},
		Handle: func(_ context.Context, _ extension.Runtime, op *extension.Operation) error {
			var body map[string]any
			if err := json.Unmarshal(op.Body, &body); err != nil {
				return err
			}
			body["priority"] = "high"
			changed, err := json.Marshal(body)
			op.Body = changed
			return err
		},
	}}})
	_, received := hookedCreateDeal(t, `{"name":"Fleet"}`)
	if !strings.Contains(received, `"priority":"high"`) {
		t.Fatalf("handler read %s, want the hook's body", received)
	}
}

func TestAnOperationHookRefusalNeverReachesCore(t *testing.T) {
	withComposed(t, extension.Extension{Name: "acme", OperationHooks: []extension.OperationHook{{
		Operations: []string{"createDeal"},
		Handle: func(context.Context, extension.Runtime, *extension.Operation) error {
			return errors.Join(extension.ErrInvalid, errors.New("no placeholder names"))
		},
	}}})
	rec, received := hookedCreateDeal(t, `{"name":"TBD"}`)
	if rec.Code != http.StatusUnprocessableEntity || received != "" {
		t.Fatalf("status %d, handler read %q; want 422 and no call", rec.Code, received)
	}
}
