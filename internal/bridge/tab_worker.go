package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// TabActionGroup represents a batch of sequential actions targeting one tab.
type TabActionGroup struct {
	TabID   string          `json:"tabId"`
	Actions []ActionRequest `json:"actions"`
}

// TabGroupResult holds the outcome of executing one tab's action group.
type TabGroupResult struct {
	TabID   string         `json:"tabId"`
	Results []ActionResult `json:"results"`
	Error   string         `json:"error,omitempty"`
}

// ActionResult is the per-action outcome within a group.
type ActionResult struct {
	Index   int            `json:"index"`
	Success bool           `json:"success"`
	Result  map[string]any `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// ParallelResult is the aggregated response for a parallel execution request.
type ParallelResult struct {
	Groups        []TabGroupResult `json:"groups"`
	TotalDuration string           `json:"totalDuration"`
}

// TabWorkerPool manages bounded parallel execution across tabs.
// Each tab group runs in its own goroutine but the total concurrency
// is limited by a semaphore sized to maxWorkers.
type TabWorkerPool struct {
	maxWorkers int
	sem        chan struct{}
}

// NewTabWorkerPool creates a pool that allows at most maxWorkers concurrent
// tab goroutines. If maxWorkers <= 0 it defaults to 4.
func NewTabWorkerPool(maxWorkers int) *TabWorkerPool {
	if maxWorkers <= 0 {
		maxWorkers = 4
	}
	return &TabWorkerPool{
		maxWorkers: maxWorkers,
		sem:        make(chan struct{}, maxWorkers),
	}
}

// MaxWorkers returns the concurrency limit.
func (p *TabWorkerPool) MaxWorkers() int { return p.maxWorkers }

// RunParallel fans out each TabActionGroup to a goroutine (bounded by the
// semaphore), executes actions sequentially within each tab, and collects
// results. The parent context controls overall cancellation/timeout.
//
// tabCtxFn resolves a tabID to its chromedp context and canonical ID.
// execFn runs a single action on a tab context.
func (p *TabWorkerPool) RunParallel(
	ctx context.Context,
	groups []TabActionGroup,
	tabCtxFn func(tabID string) (context.Context, string, error),
	execFn func(ctx context.Context, kind string, req ActionRequest) (map[string]any, error),
	actionTimeout time.Duration,
) *ParallelResult {
	start := time.Now()

	results := make([]TabGroupResult, len(groups))
	var wg sync.WaitGroup

	for i, grp := range groups {
		wg.Add(1)
		go func(idx int, g TabActionGroup) {
			defer wg.Done()

			// Acquire semaphore slot — respect parent context cancellation
			select {
			case p.sem <- struct{}{}:
				defer func() { <-p.sem }()
			case <-ctx.Done():
				results[idx] = TabGroupResult{
					TabID: g.TabID,
					Error: fmt.Sprintf("cancelled before start: %v", ctx.Err()),
				}
				return
			}

			results[idx] = p.executeGroup(ctx, g, tabCtxFn, execFn, actionTimeout)
		}(i, grp)
	}

	wg.Wait()

	return &ParallelResult{
		Groups:        results,
		TotalDuration: time.Since(start).Round(time.Millisecond).String(),
	}
}

// executeGroup runs a single tab's action list sequentially.
func (p *TabWorkerPool) executeGroup(
	ctx context.Context,
	group TabActionGroup,
	tabCtxFn func(tabID string) (context.Context, string, error),
	execFn func(ctx context.Context, kind string, req ActionRequest) (map[string]any, error),
	actionTimeout time.Duration,
) TabGroupResult {
	result := TabGroupResult{
		TabID:   group.TabID,
		Results: make([]ActionResult, 0, len(group.Actions)),
	}

	tabCtx, resolvedID, err := tabCtxFn(group.TabID)
	if err != nil {
		result.Error = fmt.Sprintf("tab context: %v", err)
		return result
	}

	slog.Debug("parallel worker started", "tabId", resolvedID, "actions", len(group.Actions))

	for i, action := range group.Actions {
		// Check parent cancellation between actions
		if ctx.Err() != nil {
			result.Results = append(result.Results, ActionResult{
				Index: i, Success: false,
				Error: fmt.Sprintf("cancelled: %v", ctx.Err()),
			})
			break
		}

		if action.Kind == "" {
			result.Results = append(result.Results, ActionResult{
				Index: i, Success: false,
				Error: "missing required field 'kind'",
			})
			continue
		}

		tCtx, tCancel := context.WithTimeout(tabCtx, actionTimeout)
		actionRes, execErr := execFn(tCtx, action.Kind, action)
		tCancel()

		if execErr != nil {
			result.Results = append(result.Results, ActionResult{
				Index: i, Success: false,
				Error: fmt.Sprintf("action %s: %v", action.Kind, execErr),
			})
			continue
		}

		result.Results = append(result.Results, ActionResult{
			Index: i, Success: true, Result: actionRes,
		})
	}

	slog.Debug("parallel worker finished", "tabId", resolvedID, "results", len(result.Results))
	return result
}
