package orchestrator

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/pinchtab/pinchtab/internal/activity"
	"github.com/pinchtab/pinchtab/internal/handlers"
	"github.com/pinchtab/pinchtab/internal/session"
)

var sessionTabsCloseBudget = 10 * time.Second

func (o *Orchestrator) SessionLifecycleHook() session.LifecycleHook {
	if o == nil {
		return func(session.LifecycleEvent) {}
	}
	return func(evt session.LifecycleEvent) {
		if evt.SessionID == "" {
			return
		}
		o.bindings.ClearSession(evt.SessionID)
		o.sessionCloses.Add(1)
		go func() {
			defer o.sessionCloses.Done()
			o.closeEndedSessionTabs(evt.SessionID)
		}()
	}
}

func (o *Orchestrator) closeEndedSessionTabs(sessionID string) {
	o.mu.RLock()
	var targets []*InstanceInternal
	for _, inst := range o.instances {
		if inst.Status == "running" && instanceIsActive(inst) && o.hopIsTrusted(inst) {
			targets = append(targets, inst)
		}
	}
	o.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), sessionTabsCloseBudget)
	defer cancel()
	header := http.Header{}
	header.Set(activity.HeaderPTSessionID, sessionID)
	var wg sync.WaitGroup
	for _, inst := range targets {
		wg.Add(1)
		go func(inst *InstanceInternal) {
			defer wg.Done()
			resp, err := o.instanceRequest(ctx, http.MethodPost, inst, handlers.SessionTabsClosePath, header)
			if err != nil {
				slog.Warn("could not close an ended session's tabs", "sessionId", sessionID, "instanceId", inst.ID, "err", err)
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				slog.Warn("could not close an ended session's tabs", "sessionId", sessionID, "instanceId", inst.ID, "status", resp.StatusCode)
			}
		}(inst)
	}
	wg.Wait()
}
