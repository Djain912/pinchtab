package scheduler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/pinchtab/pinchtab/internal/activity"
	"github.com/pinchtab/pinchtab/internal/handlers"
)

type captureRecorder struct {
	mu     sync.Mutex
	events []activity.Event
}

func (c *captureRecorder) Enabled() bool { return true }
func (c *captureRecorder) Record(e activity.Event) error {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
	return nil
}
func (c *captureRecorder) Query(activity.Filter) ([]activity.Event, error) { return nil, nil }
func (c *captureRecorder) last() (activity.Event, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return activity.Event{}, false
	}
	return c.events[len(c.events)-1], true
}

type fixedResolver struct{ port string }

func (f fixedResolver) ResolveTabInstance(string) (string, error) { return f.port, nil }

// The scheduler must record its actions as "scheduler", not "client": it now carries the
// trusted internal token so its X-PinchTab-Source and X-PinchTab-Tab-Id survive the ingress
// strip layer. Driven through the real TrustedInternalProxyStripMiddleware (the layer the
// bridge mounts) plus activity.Middleware (the recorder). On HEAD the executor sends no
// token, the strip drops both headers, and the source falls back to "client".
func TestSchedulerActionSurvivesIngressAndRecordsAsScheduler(t *testing.T) {
	const secret = "test-internal-token"
	t.Setenv("PINCHTAB_INTERNAL_TOKEN", secret)

	var gotSource, gotTabID string
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSource = r.Header.Get(activity.HeaderPTSource)
		gotTabID = r.Header.Get(activity.HeaderPTTabID)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	rec := &captureRecorder{}
	handler := handlers.TrustedInternalProxyStripMiddleware(secret)(activity.Middleware(rec, "fallback", final))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	parsed, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	exec := &actionEndpointExecutor{resolver: fixedResolver{port: parsed.Port()}, client: srv.Client()}
	if _, err := exec.Execute(context.Background(), &Task{Action: "click", Ref: "e5", TabID: "tab-42"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gotSource != "scheduler" {
		t.Errorf("inbound X-PinchTab-Source = %q, want scheduler (the internal token must let it survive the strip layer)", gotSource)
	}
	if gotTabID != "tab-42" {
		t.Errorf("inbound X-PinchTab-Tab-Id = %q, want tab-42 (the tab id header must survive, not fall back to the path)", gotTabID)
	}
	evt, ok := rec.last()
	if !ok {
		t.Fatal("no activity event recorded")
	}
	if evt.Source != "scheduler" {
		t.Errorf("recorded activity source = %q, want scheduler", evt.Source)
	}
	if evt.TabID != "tab-42" {
		t.Errorf("recorded activity tab id = %q, want tab-42", evt.TabID)
	}
}

func TestBuildActionBodyEnvelope(t *testing.T) {
	task := &Task{
		Action:   "click",
		Ref:      "e5",
		Selector: "css:button",
		TabID:    "tab-1",
		Params:   map[string]any{"button": "left", "x": 10},
	}

	body := buildActionBody(task)

	if body["kind"] != "click" {
		t.Errorf("kind = %v, want click", body["kind"])
	}
	if body["ref"] != "e5" {
		t.Errorf("ref = %v, want e5", body["ref"])
	}
	if body["selector"] != "css:button" {
		t.Errorf("selector = %v, want css:button", body["selector"])
	}
	if body["button"] != "left" {
		t.Errorf("button = %v, want left (Params passthrough)", body["button"])
	}
	if body["x"] != 10 {
		t.Errorf("x = %v, want 10 (Params passthrough)", body["x"])
	}
}

func TestBuildActionBodyReservedKeysNotOverlaid(t *testing.T) {
	// A caller must not be able to clobber envelope fields via Params.
	task := &Task{
		Action:   "click",
		Ref:      "e5",
		Selector: "css:button",
		TabID:    "tab-1",
		Params: map[string]any{
			"kind":     "evil",
			"ref":      "e999",
			"tabId":    "other-tab",
			"selector": "css:hacked",
			"safe":     "kept",
		},
	}

	body := buildActionBody(task)

	if body["kind"] != "click" {
		t.Errorf("kind = %v, want click (Params must not overlay)", body["kind"])
	}
	if body["ref"] != "e5" {
		t.Errorf("ref = %v, want e5 (Params must not overlay)", body["ref"])
	}
	if body["selector"] != "css:button" {
		t.Errorf("selector = %v, want css:button (Params must not overlay)", body["selector"])
	}
	if _, ok := body["tabId"]; ok {
		t.Errorf("tabId leaked from Params into body: %v", body["tabId"])
	}
	if body["safe"] != "kept" {
		t.Errorf("safe = %v, want kept (non-reserved Params pass through)", body["safe"])
	}
}

func TestBuildActionBodyOmitsEmptyEnvelope(t *testing.T) {
	task := &Task{Action: "snapshot"}
	body := buildActionBody(task)
	if _, ok := body["ref"]; ok {
		t.Error("empty ref should be omitted")
	}
	if _, ok := body["selector"]; ok {
		t.Error("empty selector should be omitted")
	}
}
