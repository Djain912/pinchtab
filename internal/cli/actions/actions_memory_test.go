package actions

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func memoryCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "memory"}
	cmd.Flags().Bool("gc", false, "")
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().String("tab", "", "")
	cmd.Flags().String("out", "", "")
	cmd.Flags().Int("top", 0, "")
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd
}

func TestMemoryUsageSendsGCAndTabAndPrintsTheCounters(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.RequestURI()
		_, _ = io.WriteString(w, `{"tabId":"t1","usedJSHeapSize":2097152,"totalJSHeapSize":4194304,"jsHeapSizeLimit":4294967296,"documents":1,"nodes":42,"listeners":3,"frames":1,"gc":true}`)
	}))
	defer srv.Close()

	out := captureStdout(t, func() {
		Memory(srv.Client(), srv.URL, "", memoryCommand(t, "--gc", "--tab", "t1"))
	})
	if got != "GET /memory?gc=true&tabId=t1" {
		t.Fatalf("request = %q", got)
	}
	for _, want := range []string{"heap used 2.0 MB of 4.0 MB", "(after gc)", "nodes=42", "listeners=3"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestMemorySnapshotCopiesToOutAndReportsTheLocalPath(t *testing.T) {
	serverFile := filepath.Join(t.TempDir(), "heap_1.heapsnapshot")
	if err := os.WriteFile(serverFile, []byte(`{"snapshot":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/memory/snapshot" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		encoded, _ := json.Marshal(map[string]any{"id": "heap_1", "path": serverFile, "bytes": 15, "nodeCount": 9, "durationMs": 12, "tabId": "t1"})
		_, _ = w.Write(encoded)
	}))
	defer srv.Close()

	local := filepath.Join(t.TempDir(), "copy.heapsnapshot")
	out := captureStdout(t, func() {
		MemorySnapshot(srv.Client(), srv.URL, "", memoryCommand(t, "--tab", "t1", "--out", local))
	})
	if body["tabId"] != "t1" {
		t.Fatalf("body = %v, want the tab forwarded", body)
	}
	copied, err := os.ReadFile(local)
	if err != nil || string(copied) != `{"snapshot":{}}` {
		t.Fatalf("copy = %q, %v", copied, err)
	}
	if !strings.Contains(out, "heap_1 → "+local) || !strings.Contains(out, "pinchtab memory summary heap_1") {
		t.Fatalf("output = %s", out)
	}
}

func TestMemorySummaryPrintsTheConstructorTable(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RequestURI()
		_, _ = io.WriteString(w, `{"id":"heap_1","nodeCount":20,"edgeCount":6,"totalSelfSize":3536,
			"topBySize":[{"name":"Array","count":3,"selfSize":1664}],
			"topByCount":[{"name":"(string)","count":8,"selfSize":228}],
			"duplicateStrings":[{"value":"secret","length":6,"count":3,"selfSize":120}]}`)
	}))
	defer srv.Close()

	out := captureStdout(t, func() {
		MemorySummary(srv.Client(), srv.URL, "", memoryCommand(t, "--top", "5"), "heap_1")
	})
	if got != "/memory/snapshot/heap_1/summary?top=5" {
		t.Fatalf("request = %q", got)
	}
	for _, want := range []string{"heap_1: 20 nodes, 6 edges", "CONSTRUCTOR", "Array", "(string)", `"secret"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
