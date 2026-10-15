// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

// Core's services a unit may decorate, and the ones it may provide, are part
// of the published extension surface.
//
//margince:extension-surface

package extension

import (
	"context"

	"github.com/margince/margince/backend/pkg/extension/crm"
)

// DealService is core's deal logic as a unit decorates it. Every way a deal
// is created reaches it: the web app, an agent tool and an import alike.
type DealService interface {
	// CreateDeal creates a deal. A decorator's Runtime.Tx joins the deal's
	// own transaction. So a refusal, or a failed write of the unit's, rolls
	// the deal back too.
	CreateDeal(ctx context.Context, in crm.CreateDealRequest) (crm.Deal, error)
}

// DealDecorator wraps next with a unit's logic: refuse, change the request,
// call next, then add the unit's own writes. rt is the unit's runtime for this
// one call. Decorators run in unit-name order, the first outermost.
type DealDecorator func(rt Runtime, next DealService) DealService

// DealScore is a provided DealScorer's reading of one deal.
type DealScore struct {
	// Score is 0 to 100.
	Score int
	// Reason says, in a sentence, what the score rests on.
	Reason string
}

// DealScorer is a service core has no implementation of: a unit that provides
// one scores the deal a reader opens. One scorer serves an installation.
type DealScorer func(ctx context.Context, rt Runtime, deal crm.Deal) (DealScore, error)
