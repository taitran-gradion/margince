// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

// The services a unit may provide, which core has no implementation of, are
// part of the published extension surface.
//
//margince:extension-surface

package extension

import (
	"context"

	"github.com/margince/margince/backend/pkg/extension/crm"
)

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
