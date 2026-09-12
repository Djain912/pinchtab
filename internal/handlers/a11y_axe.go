package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/pinchtab/pinchtab/internal/assets"
	"github.com/pinchtab/pinchtab/internal/audit"
	"github.com/pinchtab/pinchtab/internal/bridge"
	"github.com/pinchtab/pinchtab/internal/httpx"
)

// runAxeAudit runs vendored axe-core in the isolated world, shapes the result
// into the audit envelope, ref-maps offending nodes back to snapshot refs, and
// writes the response. The isolated world is what makes the run tamper-proof:
// page script that overrides window.axe or a DOM prototype cannot reach it.
func (h *Handlers) runAxeAudit(w http.ResponseWriter, r *http.Request, tCtx context.Context, resolvedTabID string) {
	q := r.URL.Query()
	tags := splitCSVParam(q.Get("tags"))
	rules := splitCSVParam(q.Get("rules"))
	includeIncomplete := parseBoolQuery(q.Get("includeIncomplete"))
	selector := strings.TrimSpace(q.Get("selector"))

	config, err := audit.BuildAxeRunConfig(tags, rules)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, fmt.Errorf("build axe config: %w", err))
		return
	}

	expression := assets.AxeJS + "\n;" + axeRunSnippet(config, selector)
	var raw audit.AxeRawResult
	if err := bridge.EvaluateInIsolatedWorld(tCtx, "", expression, &raw); err != nil {
		httpx.Error(w, http.StatusInternalServerError, fmt.Errorf("run axe: %w", err))
		return
	}

	report := audit.BuildAxeReport(raw, assets.AxeVersion, includeIncomplete)
	h.fillAxeRefs(tCtx, &report)

	httpx.JSON(w, 200, struct {
		TabID string `json:"tabId"`
		audit.AxeReport
	}{resolvedTabID, report})
}

// axeRunSnippet builds the in-page call: run axe over the document (or a
// selector scope), await it, and return the fields the report needs, reducing
// the large passes/inapplicable node lists to counts before they cross the wire.
func axeRunSnippet(config, selector string) string {
	target := "document"
	if selector != "" {
		target = fmt.Sprintf("(document.querySelector(%q) || document)", selector)
	}
	return fmt.Sprintf(`(function(){
  return axe.run(%s, %s).then(function(r){
    return {
      url: r.url || location.href,
      testEngine: r.testEngine,
      violations: r.violations,
      incomplete: r.incomplete,
      passes: (r.passes || []).length,
      inapplicable: (r.inapplicable || []).length
    };
  });
})()`, target, config)
}

// fillAxeRefs maps each offending node's deepest CSS target to a snapshot ref, so
// an agent can actuate a failing element directly. A node whose target does not
// resolve to a snapshot ref (a cross-origin iframe, a node not in the tree) is
// left without one rather than guessed.
func (h *Handlers) fillAxeRefs(tCtx context.Context, report *audit.AxeReport) {
	backendToRef := h.snapshotBackendRefs(tCtx)
	if len(backendToRef) == 0 {
		return
	}
	fill := func(violations []audit.AxeViolation) {
		for i := range violations {
			for j := range violations[i].Nodes {
				node := &violations[i].Nodes[j]
				if len(node.Target) == 0 {
					continue
				}
				selector := node.Target[len(node.Target)-1]
				backendID, err := bridge.BackendNodeIDForSelector(tCtx, "", selector)
				if err != nil || backendID == 0 {
					continue
				}
				if ref, ok := backendToRef[backendID]; ok {
					node.Ref = ref
				}
			}
		}
	}
	fill(report.Violations)
	fill(report.IncompleteViolations)
}

// snapshotBackendRefs maps backend node id → snapshot ref from a fresh snapshot,
// the same tree /snapshot mints, so an axe node's ref is one /action can use.
func (h *Handlers) snapshotBackendRefs(tCtx context.Context) map[int64]string {
	rawNodes, err := bridge.FetchAXTree(tCtx)
	if err != nil {
		return nil
	}
	nodes, _ := bridge.BuildSnapshot(rawNodes, "", -1)
	refs := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		if n.NodeID != 0 && n.Ref != "" {
			refs[n.NodeID] = n.Ref
		}
	}
	return refs
}

// splitCSVParam splits a comma list query value into trimmed, non-empty items.
func splitCSVParam(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
