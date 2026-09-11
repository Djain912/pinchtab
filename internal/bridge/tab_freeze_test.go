package bridge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/pinchtab/pinchtab/internal/config"
)

type frozenRecorder struct {
	mu    sync.Mutex
	calls []bool
}

func (r *frozenRecorder) set(_ context.Context, frozen bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, frozen)
	return nil
}

func (r *frozenRecorder) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.calls...)
}

func newFreezeTM(t *testing.T, policy string, delay time.Duration) (*TabManager, *frozenRecorder) {
	t.Helper()
	tm := NewTabManager(context.Background(), &config.RuntimeConfig{
		TabLifecyclePolicy: policy,
		TabCloseDelay:      delay,
	}, nil, nil, nil)
	rec := &frozenRecorder{}
	tm.setFrozen = rec.set
	tm.tabs["tab1"] = &TabEntry{Ctx: context.Background(), CDPID: "tab1", CreatedAt: time.Now(), LastUsed: time.Now()}
	return tm, rec
}

func frozenOrTimeout(tm *TabManager, want bool, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if tm.TabFrozen("tab1") == want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return tm.TabFrozen("tab1") == want
}

func freezeNow(t *testing.T, tm *TabManager) {
	t.Helper()
	tm.mu.RLock()
	gen := tm.tabs["tab1"].idleGen
	tm.mu.RUnlock()
	tm.freezeIdleTab("tab1", gen)
	if !tm.TabFrozen("tab1") {
		t.Fatal("tab should be frozen before the access under test")
	}
}

func TestFreezeIdleFreezesTheTabAfterTheDelay(t *testing.T) {
	tm, rec := newFreezeTM(t, "freeze_idle", 20*time.Millisecond)

	tm.ScheduleIdleLifecycle("tab1")

	if !frozenOrTimeout(tm, true, time.Second) {
		t.Fatal("tab was not frozen after closeDelay")
	}
	if got := rec.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("lifecycle calls = %v, want [frozen]", got)
	}
	if _, ok := tm.tabs["tab1"]; !ok {
		t.Fatal("freeze_idle must keep the tab open")
	}
}

func TestCloseIdleStillClosesInsteadOfFreezing(t *testing.T) {
	tm, rec := newFreezeTM(t, "close_idle", time.Hour)
	tm.ScheduleIdleLifecycle("tab1")
	tm.mu.RLock()
	gen := tm.tabs["tab1"].idleGen
	tm.mu.RUnlock()

	tm.idleFire("tab1", gen)

	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("close_idle froze the tab: %v", got)
	}
}

func TestEveryAccessPathUnfreezesAFrozenTab(t *testing.T) {
	cases := []struct {
		name   string
		access func(tm *TabManager) error
	}{
		{"tab lookup", func(tm *TabManager) error { _, _, err := tm.TabContext("tab1"); return err }},
		{"focus", func(tm *TabManager) error { _ = tm.FocusTab("tab1"); return nil }},
		{"adopt tracked target", func(tm *TabManager) error {
			_, err := tm.adoptExistingTarget(target.ID("tab1"), false)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tm, rec := newFreezeTM(t, "freeze_idle", time.Hour)
			freezeNow(t, tm)

			if err := tc.access(tm); err != nil {
				t.Fatalf("access: %v", err)
			}

			if tm.TabFrozen("tab1") {
				t.Fatal("tab still frozen after access")
			}
			if got := rec.snapshot(); len(got) != 2 || got[1] {
				t.Fatalf("lifecycle calls = %v, want [frozen active]", got)
			}
		})
	}
}

func TestAccessCancelsAPendingFreeze(t *testing.T) {
	tm, rec := newFreezeTM(t, "freeze_idle", 30*time.Millisecond)
	tm.ScheduleIdleLifecycle("tab1")

	if _, _, err := tm.TabContext("tab1"); err != nil {
		t.Fatalf("access: %v", err)
	}
	time.Sleep(90 * time.Millisecond)

	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("a tab in use was frozen: %v", got)
	}
}

func TestTabsDoingUnpolledWorkAreNeverFrozen(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, b *Bridge)
	}{
		{"handoff paused", func(t *testing.T, b *Bridge) {
			if err := b.SetTabHandoff("tab1", "", 0); err != nil {
				t.Fatal(err)
			}
		}},
		{"screencast streaming", func(t *testing.T, b *Bridge) {
			t.Cleanup(b.HoldAwake("tab1"))
		}},
		{"network interception rules", func(t *testing.T, b *Bridge) {
			b.routeMgr.mu.Lock()
			b.routeMgr.perTab["tab1"] = &tabRouteState{rules: []RouteRule{{Pattern: "*", Action: RouteActionAbort}}}
			b.routeMgr.mu.Unlock()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New(context.TODO(), nil, &config.RuntimeConfig{TabLifecyclePolicy: "freeze_idle", TabCloseDelay: 10 * time.Millisecond})
			b.wireTabManager(context.Background())
			rec := &frozenRecorder{}
			b.setFrozen = rec.set
			b.tabs["tab1"] = &TabEntry{Ctx: context.Background(), CDPID: "tab1"}
			tc.setup(t, b)

			b.ScheduleIdleLifecycle("tab1")
			time.Sleep(60 * time.Millisecond)

			if got := rec.snapshot(); len(got) != 0 {
				t.Fatalf("tab was frozen: %v", got)
			}
		})
	}
}

func TestReleasedScreencastHoldLetsTheTabFreeze(t *testing.T) {
	tm, _ := newFreezeTM(t, "freeze_idle", 10*time.Millisecond)
	release := tm.HoldAwake("tab1")
	release()

	tm.ScheduleIdleLifecycle("tab1")

	if !frozenOrTimeout(tm, true, time.Second) {
		t.Fatal("tab never froze after the hold was released")
	}
}
