package bridge

import (
	"context"
	"log/slog"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/pinchtab/pinchtab/internal/bridge/cdpops"
)

func setTabFrozen(ctx context.Context, frozen bool) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		return cdpops.SetPageFrozen(c, frozen)
	}))
}

func (tm *TabManager) SetFreezeVeto(veto func(tabID string) bool) {
	tm.freezeVeto = veto
}

func (tm *TabManager) keepsAwake(tabID string) bool {
	if tm.freezeVeto != nil && tm.freezeVeto(tabID) {
		return true
	}
	return len(tm.routeMgr.List(tabID)) > 0
}

func (tm *TabManager) HoldAwake(tabID string) (release func()) {
	tm.mu.Lock()
	entry, ok := tm.tabs[tabID]
	if ok {
		entry.awakeHolds++
	}
	tm.mu.Unlock()
	if !ok {
		return func() {}
	}
	return func() {
		tm.mu.Lock()
		entry.awakeHolds--
		tm.mu.Unlock()
	}
}

func (tm *TabManager) TabFrozen(tabID string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	entry, ok := tm.tabs[tabID]
	return ok && entry.frozen
}

func (tm *TabManager) freezeIdleTab(tabID string, gen uint64) {
	if tm.keepsAwake(tabID) {
		return
	}
	tm.lifecycleMu.Lock()
	defer tm.lifecycleMu.Unlock()

	tm.mu.Lock()
	entry, ok := tm.tabs[tabID]
	if !ok || entry.idleGen != gen || entry.Ctx == nil || entry.awakeHolds > 0 {
		tm.mu.Unlock()
		return
	}
	entry.idleTimer = nil
	entry.frozen = true
	ctx := entry.Ctx
	tm.mu.Unlock()

	if err := tm.setFrozen(ctx, true); err != nil {
		tm.mu.Lock()
		entry.frozen = false
		tm.mu.Unlock()
		slog.Debug("freeze idle tab failed", "tabId", tabID, "err", err)
		return
	}
	slog.Info("tab frozen", "tabId", tabID, "reason", "freeze_idle")
}

func (tm *TabManager) thawTab(tabID string) {
	tm.lifecycleMu.Lock()
	defer tm.lifecycleMu.Unlock()

	tm.mu.Lock()
	entry, ok := tm.tabs[tabID]
	if !ok || !entry.frozen {
		tm.mu.Unlock()
		return
	}
	entry.frozen = false
	ctx := entry.Ctx
	tm.mu.Unlock()

	if err := tm.setFrozen(ctx, false); err != nil {
		slog.Warn("unfreeze tab failed", "tabId", tabID, "err", err)
	}
}

func (tm *TabManager) touchTab(tabID string) {
	tm.mu.Lock()
	tm.accessed[tabID] = true
	entry, ok := tm.tabs[tabID]
	frozen := false
	if ok {
		entry.LastUsed = time.Now()
		if tm.freezesIdleTabs() {
			entry.stopIdleTimer()
		}
		frozen = entry.frozen
	}
	tm.mu.Unlock()
	if frozen {
		tm.thawTab(tabID)
	}
}
