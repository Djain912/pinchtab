package apiclient_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pinchtab/pinchtab/internal/cli/actions"
	"github.com/spf13/cobra"
)

// vocabServer mints a distinct vocabulary token per (tab, epoch) and records the
// token each action echoes, so a test can assert which snapshot's token a later
// action sent — the whole point of keying the token store by resolved tab.
type vocabServer struct {
	mu         sync.Mutex
	current    string
	epoch      map[string]int
	lastVocab  string
	hadVocab   bool
	lastPath   string
	httpServer *httptest.Server
}

func newVocabServer(current string) *vocabServer {
	s := &vocabServer{current: current, epoch: map[string]int{}}
	s.httpServer = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *vocabServer) tokenFor(tab string) string {
	if s.epoch[tab] == 0 {
		s.epoch[tab] = 1
	}
	return fmt.Sprintf("%s-e%d", tab, s.epoch[tab])
}

func (s *vocabServer) bumpEpoch(tab string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch[tab] == 0 {
		s.epoch[tab] = 1
	}
	s.epoch[tab]++
}

func (s *vocabServer) setCurrent(tab string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = tab
}

func (s *vocabServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.Method == http.MethodGet && r.URL.Path == "/snapshot" {
		resolved := r.URL.Query().Get("tabId")
		if resolved == "" {
			resolved = s.current
		}
		w.Header().Set("X-PinchTab-Tab-Id", resolved)
		w.Header().Set("X-PinchTab-Vocab", s.tokenFor(resolved))
		_, _ = w.Write([]byte("{}\n"))
		return
	}

	body, _ := io.ReadAll(r.Body)
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)
	s.lastPath = r.URL.Path
	s.lastVocab, s.hadVocab = "", false
	if v, ok := decoded["vocab"].(string); ok {
		s.lastVocab, s.hadVocab = v, true
	}
	_, _ = w.Write([]byte(`{"success":true}`))
}

func snapCmd(tab string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("tab", "", "")
	if tab != "" {
		_ = cmd.Flags().Set("tab", tab)
	}
	return cmd
}

func clickCmd(tab string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("tab", "", "")
	cmd.Flags().Bool("json", false, "")
	if tab != "" {
		_ = cmd.Flags().Set("tab", tab)
	}
	return cmd
}

// AC-1: an explicit action echoes the token of the snapshot that resolved the
// SAME tab, even when that snapshot was implicit. On HEAD the click reads the
// slot keyed by the "--tab X" spelling (T1) and misses the implicit snapshot's
// T2 stored under "default".
func TestExplicitClickSendsTheLatestTokenForItsResolvedTab(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv := newVocabServer("X")
	defer srv.httpServer.Close()
	base, client := srv.httpServer.URL, srv.httpServer.Client()

	actions.Snapshot(client, base, "", snapCmd("X"), "")
	srv.bumpEpoch("X")
	actions.Snapshot(client, base, "", snapCmd(""), "")
	actions.Action(client, base, "", "click", "e5", clickCmd("X"))

	if srv.lastVocab != "X-e2" {
		t.Fatalf("click sent vocab %q (hadVocab=%v), want the current token X-e2; a stale token here is the false 409 this fixes", srv.lastVocab, srv.hadVocab)
	}
	if srv.lastPath != "/tabs/X/action" {
		t.Fatalf("click went to %q, want /tabs/X/action", srv.lastPath)
	}
}

// AC-2 (mirror): an implicit snapshot resolves the current tab X; the current
// tab then moves to Y; an implicit click must not echo X's token, because the
// CLI cannot know it will resolve Y. On HEAD the click reads the shared
// "default" slot and sends X's token against Y.
func TestImplicitClickDoesNotSendAPriorTabsToken(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv := newVocabServer("X")
	defer srv.httpServer.Close()
	base, client := srv.httpServer.URL, srv.httpServer.Client()

	actions.Snapshot(client, base, "", snapCmd(""), "")
	srv.setCurrent("Y")
	actions.Action(client, base, "", "click", "e5", clickCmd(""))

	if srv.hadVocab {
		t.Fatalf("implicit click sent vocab %q; it must omit the token when it cannot know the resolved tab", srv.lastVocab)
	}
}

// AC-3: the per-tab files are replaced by one bounded file per server, written
// 0600, holding at most the N most recent tabs.
func TestVocabStoreIsOneBoundedFilePerServer(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	srv := newVocabServer("")
	defer srv.httpServer.Close()
	base, client := srv.httpServer.URL, srv.httpServer.Client()

	for i := 0; i < 50; i++ {
		actions.Snapshot(client, base, "", snapCmd(fmt.Sprintf("tab-%02d", i)), "")
	}

	files, err := filepath.Glob(filepath.Join(stateHome, "pinchtab", "vocab-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly one vocab store file per server, found %d: %v", len(files), files)
	}

	info, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("vocab store written %o, want 0600 (it holds a capability token on a possibly shared path)", perm)
	}

	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		TabID string `json:"tabId"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("vocab store is not the bounded JSON map: %v\n%s", err, data)
	}
	if len(entries) == 0 || len(entries) > 16 {
		t.Fatalf("store holds %d entries after 50 snapshots, want 1..16", len(entries))
	}
}
