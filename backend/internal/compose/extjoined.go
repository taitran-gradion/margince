// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package compose

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	crmcontracts "github.com/margince/margince/backend/internal/contracts"
	"github.com/margince/margince/backend/pkg/extension"
	"github.com/margince/margince/backend/pkg/extension/crm"
)

// joinedRuntime is the Runtime of a unit running inside a core write. Its Tx
// joins that write's transaction, so the unit's rows commit with core's.
type joinedRuntime struct {
	*callRuntime
	tx pgx.Tx
}

func (r joinedRuntime) Tx(ctx context.Context, fn func(ctx context.Context, tx extension.Tx) error) error {
	ctx, err := r.scoped(ctx)
	if err != nil {
		return err
	}
	namespace, err := extension.Name(r.unit).Namespace()
	if err != nil {
		return fmt.Errorf("compose: the unit's name has no SQL namespace: %w", err)
	}
	if err := r.enterTx(); err != nil {
		return err
	}
	defer r.leaveTx()
	return fn(ctx, r.txOn(r.tx, namespace))
}

// publicDeal is core's deal in the published shape. Both are generated from
// one contract schema, so the JSON of one is the JSON of the other.
func publicDeal(d crmcontracts.Deal) (crm.Deal, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return crm.Deal{}, err
	}
	var out crm.Deal
	return out, json.Unmarshal(raw, &out)
}
