package handlers

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pinchtab/pinchtab/internal/authn"
	"github.com/pinchtab/pinchtab/internal/browsersession"
	"github.com/pinchtab/pinchtab/internal/config"
	"github.com/pinchtab/pinchtab/internal/session"
)

func shortenStreamRevalidate(t *testing.T) {
	t.Helper()
	prev := streamRevalidateInterval
	streamRevalidateInterval = 25 * time.Millisecond
	t.Cleanup(func() { streamRevalidateInterval = prev })
}

// blockingSSE writes one event then holds the connection open until its request
// context is cancelled, standing in for every stream handler that loops on
// r.Context().Done().
func blockingSSE(started chan<- struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = fmt.Fprint(w, "data: open\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		<-r.Context().Done()
	}
}

// awaitStreamEnd reads the first event, runs revoke, and reports whether the
// body reader unblocked (EOF/err) within the bound.
func awaitStreamEnd(t *testing.T, resp *http.Response, started <-chan struct{}, revoke func()) bool {
	t.Helper()
	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("stream never delivered its first event: %v", err)
	}
	<-started

	revoke()

	ended := make(chan struct{})
	go func() {
		_, _ = reader.ReadString('\n')
		_, err := reader.ReadString('\n')
		for err == nil {
			_, err = reader.ReadString('\n')
		}
		close(ended)
	}()

	select {
	case <-ended:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}

func TestStreamEndsWhenCookieSessionRevoked(t *testing.T) {
	shortenStreamRevalidate(t)

	cfg := &config.RuntimeConfig{Token: "server-secret"}
	sessions := browsersession.NewManager(browsersession.Config{})
	sessionID, err := sessions.Create(cfg.Token)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	started := make(chan struct{})
	handler := AuthMiddlewareWithSessions(config.NewLive(cfg), sessions, nil, blockingSSE(started))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Origin", srv.URL)
	req.AddCookie(&http.Cookie{Name: authn.CookieName, Value: sessionID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream did not open: status %d", resp.StatusCode)
	}

	if !awaitStreamEnd(t, resp, started, func() { sessions.Revoke(sessionID) }) {
		t.Fatal("stream kept delivering after the cookie session was revoked (logout)")
	}
}

func TestStreamEndsWhenAgentSessionRevoked(t *testing.T) {
	shortenStreamRevalidate(t)

	cfg := &config.RuntimeConfig{Token: "server-secret"}
	store := session.NewStore(session.Config{Enabled: true, MaxLifetime: time.Hour, IdleTimeout: time.Hour})
	sessID, token, err := store.Create("agent-1", "test", "")
	if err != nil {
		t.Fatalf("create agent session: %v", err)
	}

	started := make(chan struct{})
	handler := AuthMiddlewareWithSessions(config.NewLive(cfg), nil, store, blockingSSE(started))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/network/stream", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Session "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream did not open: status %d", resp.StatusCode)
	}

	if !awaitStreamEnd(t, resp, started, func() { store.Revoke(sessID) }) {
		t.Fatal("stream kept delivering after the agent session was revoked")
	}
}

func TestNonStreamRequestSpawnsNoStreamGoroutine(t *testing.T) {
	shortenStreamRevalidate(t)

	if isStreamRequest(httptest.NewRequest(http.MethodGet, "/api/events", nil)) {
		t.Fatal("a plain GET must not be classified as a stream request")
	}
	sse := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	sse.Header.Set("Accept", "text/event-stream")
	if !isStreamRequest(sse) {
		t.Fatal("an Accept: text/event-stream request must be a stream request")
	}
	ws := httptest.NewRequest(http.MethodGet, "/screencast", nil)
	ws.Header.Set("Upgrade", "websocket")
	ws.Header.Set("Connection", "Upgrade")
	if !isStreamRequest(ws) {
		t.Fatal("a websocket upgrade must be a stream request")
	}

	cfg := &config.RuntimeConfig{Token: "server-secret"}
	sessions := browsersession.NewManager(browsersession.Config{})
	sessionID, err := sessions.Create(cfg.Token)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	handler := AuthMiddlewareWithSessions(config.NewLive(cfg), sessions, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
		req.Header.Set("Origin", "http://example.test")
		req.Host = "example.test"
		req.AddCookie(&http.Cookie{Name: authn.CookieName, Value: sessionID})
		return req
	}

	handler.ServeHTTP(httptest.NewRecorder(), newReq())
	runtime.Gosched()
	baseline := runtime.NumGoroutine()
	for i := 0; i < 200; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), newReq())
	}

	deadline := time.Now().Add(time.Second)
	for {
		runtime.Gosched()
		if runtime.NumGoroutine() <= baseline+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("non-stream requests leaked goroutines: baseline %d, now %d", baseline, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// streamSiteFloor is the set of production handlers that open a long-lived
// response — the four SSE endpoints and the screencast websocket. Every one is
// served through the front door's AuthMiddlewareWithSessions (directly or via the
// proxy it fronts), so the stream-aware credential re-check reaches them all. A
// new site must be added here deliberately, which is the point at which its
// routing behind that middleware is confirmed.
var streamSiteFloor = map[string]bool{
	"dashboard/handlers_sse.go":          true,
	"handlers/network.go":                true,
	"handlers/network_export_stream.go":  true,
	"orchestrator/handlers_instances.go": true,
	"handlers/screencast.go":             true,
}

func fileOpensStream(src string) bool {
	return strings.Contains(src, `"Content-Type", "text/event-stream"`) ||
		strings.Contains(src, "ws.UpgradeHTTP(")
}

func scanStreamSites(root string) (map[string]string, error) {
	sites := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if fileOpensStream(string(b)) {
			rel := filepath.ToSlash(path)
			sites[rel] = rel
		}
		return nil
	})
	return sites, err
}

func TestStreamSiteCensusStaysBehindStreamAwareMiddleware(t *testing.T) {
	sites, err := scanStreamSites("..")
	if err != nil {
		t.Fatalf("walk internal tree: %v", err)
	}

	matched := map[string]bool{}
	for path := range sites {
		var hit string
		for suffix := range streamSiteFloor {
			if strings.HasSuffix(path, suffix) {
				hit = suffix
				break
			}
		}
		if hit == "" {
			t.Errorf("stream site %s is not in streamSiteFloor; confirm it is served behind AuthMiddlewareWithSessions and add it", path)
			continue
		}
		matched[hit] = true
	}

	if len(matched) < len(streamSiteFloor) {
		for suffix := range streamSiteFloor {
			if !matched[suffix] {
				t.Errorf("expected stream site %s was not found; the census floor of %d is not met", suffix, len(streamSiteFloor))
			}
		}
	}

	frontDoor, err := os.ReadFile("../server/front_door_metrics.go")
	if err != nil {
		t.Fatalf("read front door wiring: %v", err)
	}
	if !strings.Contains(string(frontDoor), "AuthMiddlewareWithSessions") {
		t.Fatal("front door no longer mounts AuthMiddlewareWithSessions; the stream-aware credential re-check does not reach the stream sites")
	}
}

func TestStreamSiteCensusDetectsAPlantedSite(t *testing.T) {
	dir := t.TempDir()
	planted := filepath.Join(dir, "planted.go")
	src := "package planted\n\nimport \"net/http\"\n\nfunc H(w http.ResponseWriter, r *http.Request) {\n\tw.Header().Set(\"Content-Type\", \"text/event-stream\")\n}\n"
	if err := os.WriteFile(planted, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, err := scanStreamSites(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("census did not detect the planted SSE site: found %d sites", len(sites))
	}

	wsDir := t.TempDir()
	wsSrc := "package planted\n\nimport (\n\t\"net/http\"\n\n\t\"github.com/gobwas/ws\"\n)\n\nfunc W(w http.ResponseWriter, r *http.Request) {\n\t_, _, _, _ = ws.UpgradeHTTP(r, w)\n}\n"
	if err := os.WriteFile(filepath.Join(wsDir, "planted_ws.go"), []byte(wsSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	wsSites, err := scanStreamSites(wsDir)
	if err != nil {
		t.Fatalf("scan ws: %v", err)
	}
	if len(wsSites) != 1 {
		t.Fatalf("census did not detect the planted websocket site: found %d sites", len(wsSites))
	}
}
