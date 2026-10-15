// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package compose

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/margince/margince/backend/internal/modules/deals"
	"github.com/margince/margince/backend/pkg/extension"
	"github.com/margince/margince/backend/pkg/extension/crm"
)

func TestADealDecoratorChangesTheFieldsItMayChange(t *testing.T) {
	in := deals.CreateDealInput{Name: "Northwind", Source: "manual"}
	req := publicCreateRequest(in)
	req.Name = "Northwind (Acme)"
	high := crm.CreateDealRequestPriority("high")
	req.Priority = &high

	got, err := applyDecoratedRequest(in, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Northwind (Acme)" || got.Priority == nil || *got.Priority != "high" {
		t.Fatalf("got name %q priority %v, want the decorator's", got.Name, got.Priority)
	}
}

func TestADealDecoratorMayNotChangeWhatCoreAlreadyDecided(t *testing.T) {
	in := deals.CreateDealInput{Name: "Northwind", Source: "manual"}
	req := publicCreateRequest(in)
	req.Source = "import"

	_, err := applyDecoratedRequest(in, req)
	if !errors.Is(err, extension.ErrInvalid) || !strings.Contains(err.Error(), "only name, description and priority") {
		t.Fatalf("err = %v, want ErrInvalid naming the fields a decorator may change", err)
	}
}

func TestAnInstallationServesOneDealScorer(t *testing.T) {
	scorer := extension.DealScorer(func(context.Context, extension.Runtime, crm.Deal) (extension.DealScore, error) {
		return extension.DealScore{}, nil
	})
	one := []extension.Extension{{Name: "acme", ScoreDeals: scorer}, {Name: "beta"}}
	if err := refuseSecondDealScorer(one); err != nil {
		t.Fatalf("one scorer refused: %v", err)
	}
	if unit, ok := dealScorerUnit(one); !ok || unit.Name != "acme" {
		t.Fatalf("scorer unit = %q, %v; want acme", unit.Name, ok)
	}
	two := append(one, extension.Extension{Name: "gamma", ScoreDeals: scorer})
	if err := refuseSecondDealScorer(two); err == nil || !strings.Contains(err.Error(), "acme and gamma") {
		t.Fatalf("err = %v, want a refusal naming both providers", err)
	}
}
