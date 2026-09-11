package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/pinchtab/pinchtab/internal/bridge"
	"github.com/pinchtab/pinchtab/internal/config"
	"github.com/pinchtab/pinchtab/internal/testbrowser"
)

const pendingAlertTab = "tab-alert"

func handlersOverAPendingAlert(t *testing.T) *Handlers {
	t.Helper()
	chromePath := testbrowser.Path(t)
	profile := testbrowser.ProfileDir(t)
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.UserDataDir(profile),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
	)...)
	ctx, cancelBrowser := chromedp.NewContext(alloc)
	ctx, cancelTimeout := context.WithTimeout(ctx, 40*time.Second)
	t.Cleanup(func() {
		cancelTimeout()
		cancelBrowser()
		cancelAlloc()
		_ = os.RemoveAll(profile)
	})
	html := `<button id="b" onclick="alert('hi')">b</button><button id="c">c</button>`
	if err := chromedp.Run(ctx, chromedp.Navigate("data:text/html;base64,"+base64.StdEncoding.EncodeToString([]byte(html)))); err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{}, 1)
	chromedp.ListenTarget(ctx, func(ev any) {
		if _, ok := ev.(*page.EventJavascriptDialogOpening); ok {
			select {
			case opened <- struct{}{}:
			default:
			}
		}
	})
	if err := chromedp.Run(ctx, chromedp.Evaluate(`setTimeout(() => document.getElementById('b').click(), 0)`, nil)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the alert never opened")
	}
	cfg := &config.RuntimeConfig{ActionTimeout: time.Second, AllowMacro: true, DefaultBrowser: config.BrowserChrome, StateDir: t.TempDir()}
	b := bridge.New(context.Background(), ctx, cfg)
	b.RegisterTab(pendingAlertTab, ctx)
	b.GetDialogManager().SetPending(pendingAlertTab, &bridge.DialogState{Type: "alert", Message: "hi"})
	return New(b, cfg, nil, nil, nil)
}

func TestAStepWhoseSelectorResolutionTimesOutBehindADialogCarriesTheRemedyOnce(t *testing.T) {
	h := handlersOverAPendingAlert(t)
	for _, tc := range []struct{ name, path, body string }{
		{"batch", "/actions", `{"tabId":"` + pendingAlertTab + `","actions":[{"kind":"click","selector":"#c"}]}`},
		{"macro", "/macro", `{"tabId":"` + pendingAlertTab + `","steps":[{"kind":"click","selector":"#c"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			if tc.path == "/macro" {
				h.HandleMacro(rec, req)
			} else {
				h.HandleActions(rec, req)
			}
			var envelope batchEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode: %v: %s", err, rec.Body.String())
			}
			if len(envelope.Results) != 1 || envelope.Results[0].Success {
				t.Fatalf("want one failed result: %s", rec.Body.String())
			}
			got := envelope.Results[0].Error
			if !strings.Contains(got, "timed out") || !strings.Contains(got, `"hi"`) {
				t.Fatalf("step error does not name the blocking dialog: %q", got)
			}
			if n := strings.Count(got, dialogBlockedStepRemedy); n != 1 {
				t.Fatalf("remedy sentence appears %d times, want 1: %q", n, got)
			}
		})
	}
}
