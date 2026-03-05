package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pinchtab/pinchtab/internal/bridge"
	"github.com/pinchtab/pinchtab/internal/web"
)

type parallelRequest struct {
	Groups      []bridge.TabActionGroup `json:"groups"`
	Owner       string                  `json:"owner"`
	StopOnError bool                    `json:"stopOnError"`
}

// HandleParallelActions executes action groups across multiple tabs concurrently.
// Each group targets one tab and its actions run sequentially within that tab,
// but different tab groups run in parallel bounded by BRIDGE_MAX_PARALLEL_TABS.
//
// @Endpoint POST /tabs/parallel
func (h *Handlers) HandleParallelActions(w http.ResponseWriter, r *http.Request) {
	if err := h.ensureChrome(); err != nil {
		web.Error(w, 500, fmt.Errorf("chrome initialization: %w", err))
		return
	}

	var req parallelRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize)).Decode(&req); err != nil {
		web.Error(w, 400, fmt.Errorf("decode: %w", err))
		return
	}

	if len(req.Groups) == 0 {
		web.Error(w, 400, fmt.Errorf("groups array is empty"))
		return
	}

	// Reject duplicate tab IDs — concurrent actions on the same tab would
	// race against each other within the chromedp target session.
	seen := make(map[string]bool, len(req.Groups))
	for _, g := range req.Groups {
		if g.TabID == "" {
			web.Error(w, 400, fmt.Errorf("each group must specify a tabId"))
			return
		}
		if seen[g.TabID] {
			web.Error(w, 400, fmt.Errorf("duplicate tabId %q — use one group per tab", g.TabID))
			return
		}
		seen[g.TabID] = true
		if len(g.Actions) == 0 {
			web.Error(w, 400, fmt.Errorf("group for tab %q has no actions", g.TabID))
			return
		}
	}

	// Enforce tab leases for all targeted tabs before starting parallel work.
	// Check both the hash-based tab ID (used by /tab/lock) and the resolved
	// CDP target ID — locks may be stored under either form.
	owner := resolveOwner(r, req.Owner)
	for _, g := range req.Groups {
		_, resolvedID, err := h.Bridge.TabContext(g.TabID)
		if err != nil {
			web.Error(w, 404, fmt.Errorf("tab %s: %w", g.TabID, err))
			return
		}
		if err := h.enforceTabLease(g.TabID, owner); err != nil {
			web.ErrorCode(w, 423, "tab_locked", err.Error(), false, nil)
			return
		}
		if resolvedID != g.TabID {
			if err := h.enforceTabLease(resolvedID, owner); err != nil {
				web.ErrorCode(w, 423, "tab_locked", err.Error(), false, nil)
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.Config.ActionTimeout*3)
	defer cancel()

	result := h.Bridge.RunParallel(ctx, req.Groups, h.Config.ActionTimeout)

	web.JSON(w, 200, result)
}
