package bridge

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewTabWorkerPool_DefaultWorkers(t *testing.T) {
	pool := NewTabWorkerPool(0)
	if pool.MaxWorkers() != 4 {
		t.Fatalf("expected default 4 workers, got %d", pool.MaxWorkers())
	}
}

func TestNewTabWorkerPool_NegativeWorkers(t *testing.T) {
	pool := NewTabWorkerPool(-1)
	if pool.MaxWorkers() != 4 {
		t.Fatalf("expected default 4 workers, got %d", pool.MaxWorkers())
	}
}

func TestNewTabWorkerPool_CustomWorkers(t *testing.T) {
	pool := NewTabWorkerPool(8)
	if pool.MaxWorkers() != 8 {
		t.Fatalf("expected 8 workers, got %d", pool.MaxWorkers())
	}
}

func TestRunParallel_BasicTwoGroups(t *testing.T) {
	pool := NewTabWorkerPool(4)

	groups := []TabActionGroup{
		{TabID: "tab_a", Actions: []ActionRequest{{Kind: "click", Selector: "#btn"}}},
		{TabID: "tab_b", Actions: []ActionRequest{{Kind: "type", Selector: "#in", Text: "hi"}}},
	}

	tabCtxFn := func(tabID string) (context.Context, string, error) {
		return context.Background(), tabID, nil
	}
	execFn := func(ctx context.Context, kind string, req ActionRequest) (map[string]any, error) {
		return map[string]any{"kind": kind, "tab": req.TabID}, nil
	}

	result := pool.RunParallel(context.Background(), groups, tabCtxFn, execFn, 5*time.Second)

	if len(result.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(result.Groups))
	}
	for _, g := range result.Groups {
		if g.Error != "" {
			t.Fatalf("group %s had error: %s", g.TabID, g.Error)
		}
		if len(g.Results) != 1 {
			t.Fatalf("expected 1 result for %s, got %d", g.TabID, len(g.Results))
		}
		if !g.Results[0].Success {
			t.Fatalf("expected success for %s, got error: %s", g.TabID, g.Results[0].Error)
		}
	}
	if result.TotalDuration == "" {
		t.Fatal("expected non-empty total duration")
	}
}

func TestRunParallel_TabContextError(t *testing.T) {
	pool := NewTabWorkerPool(2)

	groups := []TabActionGroup{
		{TabID: "bad_tab", Actions: []ActionRequest{{Kind: "click"}}},
	}

	tabCtxFn := func(tabID string) (context.Context, string, error) {
		return nil, "", fmt.Errorf("tab %s not found", tabID)
	}
	execFn := func(ctx context.Context, kind string, req ActionRequest) (map[string]any, error) {
		return nil, nil
	}

	result := pool.RunParallel(context.Background(), groups, tabCtxFn, execFn, 5*time.Second)

	if result.Groups[0].Error == "" {
		t.Fatal("expected error for bad tab")
	}
}

func TestRunParallel_ActionError(t *testing.T) {
	pool := NewTabWorkerPool(2)

	groups := []TabActionGroup{
		{TabID: "tab_a", Actions: []ActionRequest{
			{Kind: "click", Selector: "#ok"},
			{Kind: "click", Selector: "#fail"},
			{Kind: "click", Selector: "#ok2"},
		}},
	}

	tabCtxFn := func(tabID string) (context.Context, string, error) {
		return context.Background(), tabID, nil
	}
	execFn := func(ctx context.Context, kind string, req ActionRequest) (map[string]any, error) {
		if req.Selector == "#fail" {
			return nil, fmt.Errorf("element not found")
		}
		return map[string]any{"ok": true}, nil
	}

	result := pool.RunParallel(context.Background(), groups, tabCtxFn, execFn, 5*time.Second)

	g := result.Groups[0]
	if len(g.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(g.Results))
	}
	if !g.Results[0].Success {
		t.Fatal("first action should succeed")
	}
	if g.Results[1].Success {
		t.Fatal("second action should fail")
	}
	if !g.Results[2].Success {
		t.Fatal("third action should succeed (continues after error)")
	}
}

func TestRunParallel_MissingKind(t *testing.T) {
	pool := NewTabWorkerPool(2)

	groups := []TabActionGroup{
		{TabID: "tab_a", Actions: []ActionRequest{{Kind: ""}}},
	}

	tabCtxFn := func(tabID string) (context.Context, string, error) {
		return context.Background(), tabID, nil
	}
	execFn := func(ctx context.Context, kind string, req ActionRequest) (map[string]any, error) {
		t.Fatal("should not be called for empty kind")
		return nil, nil
	}

	result := pool.RunParallel(context.Background(), groups, tabCtxFn, execFn, 5*time.Second)

	if result.Groups[0].Results[0].Success {
		t.Fatal("expected failure for empty kind")
	}
	if result.Groups[0].Results[0].Error != "missing required field 'kind'" {
		t.Fatalf("unexpected error: %s", result.Groups[0].Results[0].Error)
	}
}

func TestRunParallel_ContextCancelled(t *testing.T) {
	pool := NewTabWorkerPool(1) // single worker to ensure predictable ordering

	ctx, cancel := context.WithCancel(context.Background())

	groups := []TabActionGroup{
		{TabID: "tab_a", Actions: []ActionRequest{{Kind: "click"}, {Kind: "type"}}},
		{TabID: "tab_b", Actions: []ActionRequest{{Kind: "click"}}},
	}

	var callCount atomic.Int32
	tabCtxFn := func(tabID string) (context.Context, string, error) {
		return ctx, tabID, nil
	}
	execFn := func(c context.Context, kind string, req ActionRequest) (map[string]any, error) {
		n := callCount.Add(1)
		if n == 1 {
			cancel() // cancel after first action
			time.Sleep(10 * time.Millisecond)
		}
		return map[string]any{"ok": true}, nil
	}

	result := pool.RunParallel(ctx, groups, tabCtxFn, execFn, 5*time.Second)

	// At least one group should have a cancellation error or reduced results
	hasCancellation := false
	for _, g := range result.Groups {
		if g.Error != "" {
			hasCancellation = true
			break
		}
		for _, r := range g.Results {
			if !r.Success {
				hasCancellation = true
				break
			}
		}
	}
	if !hasCancellation {
		t.Fatal("expected cancellation to affect at least one group")
	}
}

func TestRunParallel_BoundedConcurrency(t *testing.T) {
	pool := NewTabWorkerPool(2) // only 2 workers

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	groups := make([]TabActionGroup, 6)
	for i := range groups {
		groups[i] = TabActionGroup{
			TabID:   fmt.Sprintf("tab_%d", i),
			Actions: []ActionRequest{{Kind: "click"}},
		}
	}

	tabCtxFn := func(tabID string) (context.Context, string, error) {
		return context.Background(), tabID, nil
	}
	execFn := func(ctx context.Context, kind string, req ActionRequest) (map[string]any, error) {
		c := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if c <= old || maxConcurrent.CompareAndSwap(old, c) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		concurrent.Add(-1)
		return map[string]any{"ok": true}, nil
	}

	result := pool.RunParallel(context.Background(), groups, tabCtxFn, execFn, 5*time.Second)

	for _, g := range result.Groups {
		if g.Error != "" {
			t.Fatalf("unexpected group error: %s", g.Error)
		}
	}

	mc := maxConcurrent.Load()
	if mc > 2 {
		t.Fatalf("expected max concurrency <= 2, got %d", mc)
	}
	if mc < 2 {
		t.Fatalf("expected max concurrency == 2 (pool didn't parallelize), got %d", mc)
	}
}

func TestRunParallel_EmptyGroups(t *testing.T) {
	pool := NewTabWorkerPool(2)

	result := pool.RunParallel(
		context.Background(),
		[]TabActionGroup{},
		func(string) (context.Context, string, error) { return nil, "", nil },
		func(context.Context, string, ActionRequest) (map[string]any, error) { return nil, nil },
		5*time.Second,
	)

	if len(result.Groups) != 0 {
		t.Fatalf("expected 0 groups, got %d", len(result.Groups))
	}
}

func TestRunParallel_MultipleActionsPerTab(t *testing.T) {
	pool := NewTabWorkerPool(4)

	groups := []TabActionGroup{
		{TabID: "tab_a", Actions: []ActionRequest{
			{Kind: "click", Selector: "#a"},
			{Kind: "type", Selector: "#b", Text: "hello"},
			{Kind: "click", Selector: "#c"},
		}},
	}

	var order []string
	tabCtxFn := func(tabID string) (context.Context, string, error) {
		return context.Background(), tabID, nil
	}
	execFn := func(ctx context.Context, kind string, req ActionRequest) (map[string]any, error) {
		order = append(order, req.Selector)
		return map[string]any{"sel": req.Selector}, nil
	}

	result := pool.RunParallel(context.Background(), groups, tabCtxFn, execFn, 5*time.Second)

	g := result.Groups[0]
	if len(g.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(g.Results))
	}

	// Actions within a tab must execute sequentially in order
	if len(order) != 3 || order[0] != "#a" || order[1] != "#b" || order[2] != "#c" {
		t.Fatalf("expected sequential order [#a, #b, #c], got %v", order)
	}
}
