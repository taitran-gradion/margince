// SPDX-License-Identifier: BUSL-1.1
// SPDX-FileCopyrightText: 2026 Gradion

package compose

import (
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	crmcontracts "github.com/margince/margince/backend/internal/contracts"
	"github.com/margince/margince/backend/internal/modules/deals"
	"github.com/margince/margince/backend/internal/platform/database/storekit"
	"github.com/margince/margince/backend/internal/platform/httperr"
	"github.com/margince/margince/backend/internal/shared/kernel/ids"
	"github.com/margince/margince/backend/pkg/extension"
)

// dealScoreHandlers serves GET /deals/{id}/score from the scorer a composed
// unit provides. Core has none, so an installation without one answers 404.
type dealScoreHandlers struct {
	deals *deals.Store
}

func newDealScoreHandlers(pool *pgxpool.Pool) dealScoreHandlers {
	return dealScoreHandlers{deals: deals.NewStore(InstallationDB(pool), DealsInstallation())}
}

// GetDealScore implements GET /deals/{id}/score. The deal is read first, so a
// deal the caller cannot see answers as every deal read does.
func (h dealScoreHandlers) GetDealScore(w http.ResponseWriter, r *http.Request, id crmcontracts.Id) {
	ctx := r.Context()
	deal, err := h.deals.GetDeal(ctx, ids.From[ids.DealKind](ids.UUID(id)), storekit.IncludeArchived)
	if err != nil {
		httperr.Write(w, r, err)
		return
	}
	unit, ok := dealScorerUnit(ComposedExtensions())
	if !ok {
		httperr.Write(w, r, &httperr.DetailedError{
			Status: http.StatusNotFound, Code: "no_deal_scorer",
			Detail: "no extension in this installation provides a deal scorer",
		})
		return
	}
	public, err := publicDeal(deal)
	if err != nil {
		httperr.Write(w, r, err)
		return
	}
	rt := runtimeFor(ctx, string(unit.Name), string(unit.Version), "provide/deal_scorer", boundExtensionRuntime())
	defer rt.release()
	score, err := unit.ScoreDeals(ctx, rt, public)
	if err != nil {
		httperr.Write(w, r, unitRefusal(err))
		return
	}
	if score.Score < 0 || score.Score > 100 {
		httperr.Write(w, r, fmt.Errorf("compose: %s scored a deal %d, outside 0 to 100", unit.Name, score.Score))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, crmcontracts.DealScore{Score: score.Score, Reason: score.Reason, Provider: string(unit.Name)})
}

// dealScorerUnit is the one composed unit that provides a deal scorer.
// refuseSecondDealScorer keeps it to one at boot.
func dealScorerUnit(exts []extension.Extension) (extension.Extension, bool) {
	for _, e := range exts {
		if e.ScoreDeals != nil {
			return e, true
		}
	}
	return extension.Extension{}, false
}

// refuseSecondDealScorer refuses a set in which two units provide a deal
// scorer: the answer would depend on which one the lookup met first.
func refuseSecondDealScorer(exts []extension.Extension) error {
	var first extension.Name
	for _, e := range exts {
		if e.ScoreDeals == nil {
			continue
		}
		if first != "" {
			return fmt.Errorf("extensions %s and %s both provide a deal scorer; an installation serves one", first, e.Name)
		}
		first = e.Name
	}
	return nil
}
