package handlers

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/pinchtab/pinchtab/internal/bridge"
	"github.com/pinchtab/pinchtab/internal/config"
	"github.com/pinchtab/pinchtab/internal/testbrowser"
)

func TestATabResolvedForARequestStaysAwakeUntilTheRequestEnds(t *testing.T) {
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(testbrowser.Path(t)),
		chromedp.UserDataDir(testbrowser.ProfileDir(t)),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
	)...)
	t.Cleanup(cancelAlloc)
	tabCtx, cancelTab := chromedp.NewContext(alloc)
	t.Cleanup(cancelTab)
	if err := chromedp.Run(tabCtx); err != nil {
		t.Fatalf("start browser: %v", err)
	}
	cfg := &config.RuntimeConfig{TabLifecyclePolicy: "freeze_idle", TabCloseDelay: 20 * time.Millisecond, StateDir: t.TempDir()}
	b := bridge.New(context.Background(), tabCtx, cfg)
	b.RegisterTab("tabA", tabCtx)
	h := New(b, cfg, nil, nil, nil)

	reqCtx, endRequest := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/snapshot", nil).WithContext(reqCtx)
	if _, _, err := h.tabContext(req, "tabA"); err != nil {
		t.Fatalf("resolve tab: %v", err)
	}

	time.Sleep(80 * time.Millisecond)
	if b.TabFrozen("tabA") {
		t.Fatal("tab froze while its request was still running")
	}

	endRequest()
	deadline := time.Now().Add(time.Second)
	for !b.TabFrozen("tabA") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !b.TabFrozen("tabA") {
		t.Fatal("tab never froze after its request ended")
	}
}
