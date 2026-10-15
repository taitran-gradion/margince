// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"

	crmcontracts "github.com/margince/margince/backend/internal/contracts"
	"github.com/margince/margince/backend/internal/modules/deals"
	"github.com/margince/margince/backend/pkg/extension"
	"github.com/margince/margince/backend/pkg/extension/crm"
)

// WrapDealCreate is the deals.WrapCreate every deal store runs: each composed
// unit's DecorateDeals around core's create, in unit order, the first
// outermost. It reads the composed set per call, so a store built before the
// units registered still runs them.
func WrapDealCreate(next deals.CreateInTx) deals.CreateInTx {
	create := next
	exts := ComposedExtensions()
	for i := len(exts) - 1; i >= 0; i-- {
		if exts[i].DecorateDeals != nil {
			create = decorateDealCreate(exts[i], create)
		}
	}
	return create
}

func decorateDealCreate(e extension.Extension, next deals.CreateInTx) deals.CreateInTx {
	return func(ctx context.Context, tx pgx.Tx, in deals.CreateDealInput) (crmcontracts.Deal, error) {
		rt := joinedRuntime{callRuntime: runtimeFor(ctx, string(e.Name), string(e.Version), "decorate/deals", boundExtensionRuntime()), tx: tx}
		defer rt.release()
		core := &coreDealService{tx: tx, next: next, in: in}
		if _, err := e.DecorateDeals(rt, core).CreateDeal(ctx, publicCreateRequest(in)); err != nil {
			return crmcontracts.Deal{}, unitRefusal(err)
		}
		if core.created == nil {
			return crmcontracts.Deal{}, fmt.Errorf("compose: %s's deal decorator returned without calling core's create", e.Name)
		}
		return *core.created, nil
	}
}

// joinedRuntime is a decorator's Runtime. Its Tx joins the transaction of the
// core operation it decorates, so the unit's writes commit with core's.
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
		return fmt.Errorf("compose: the decorating unit's name has no SQL namespace: %w", err)
	}
	if err := r.enterTx(); err != nil {
		return err
	}
	defer r.leaveTx()
	return fn(ctx, r.txOn(r.tx, namespace))
}

// coreDealService is what a decorator's next reaches: the rest of the chain,
// and at its end core's own create, on the deal's transaction.
type coreDealService struct {
	tx      pgx.Tx
	next    deals.CreateInTx
	in      deals.CreateDealInput
	created *crmcontracts.Deal
}

func (c *coreDealService) CreateDeal(ctx context.Context, req crm.CreateDealRequest) (crm.Deal, error) {
	in, err := applyDecoratedRequest(c.in, req)
	if err != nil {
		return crm.Deal{}, err
	}
	created, err := c.next(ctx, c.tx, in)
	if err != nil {
		return crm.Deal{}, err
	}
	c.created = &created
	return publicDeal(created)
}

// publicCreateRequest is the request a decorator sees: the fields it may
// change, and the ones it reads to decide.
func publicCreateRequest(in deals.CreateDealInput) crm.CreateDealRequest {
	req := crm.CreateDealRequest{
		Name:        in.Name,
		Description: in.Description,
		AmountMinor: in.AmountMinor,
		Currency:    in.Currency,
		PipelineId:  in.PipelineID.String(),
		StageId:     in.StageID.String(),
		Source:      in.Source,
	}
	if in.Priority != nil {
		priority := crm.CreateDealRequestPriority(*in.Priority)
		req.Priority = &priority
	}
	if in.CompanyID != nil {
		company := in.CompanyID.String()
		req.CompanyId = &company
	}
	return req
}

// applyDecoratedRequest takes a decorator's changes to the fields it may
// change, and refuses a change to any other. Those carry decisions core made
// before the decorator ran, such as who captured the deal.
func applyDecoratedRequest(in deals.CreateDealInput, req crm.CreateDealRequest) (deals.CreateDealInput, error) {
	readOnly := func(r crm.CreateDealRequest) crm.CreateDealRequest {
		r.Name, r.Description, r.Priority = "", nil, nil
		return r
	}
	if !reflect.DeepEqual(readOnly(req), readOnly(publicCreateRequest(in))) {
		return in, fmt.Errorf("%w: a deal decorator may change only name, description and priority", extension.ErrInvalid)
	}
	in.Name = req.Name
	in.Description = req.Description
	in.Priority = nil
	if req.Priority != nil {
		priority := string(*req.Priority)
		in.Priority = &priority
	}
	return in, nil
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
