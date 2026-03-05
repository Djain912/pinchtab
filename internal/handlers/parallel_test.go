package handlers

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pinchtab/pinchtab/internal/bridge"
	"github.com/pinchtab/pinchtab/internal/config"
)

func TestHandleParallelActions_EmptyGroups(t *testing.T) {
	h := New(&mockBridge{}, &config.RuntimeConfig{ActionTimeout: 5 * time.Second}, nil, nil, nil)
	body := `{"groups":[]}`
	req := httptest.NewRequest("POST", "/tabs/parallel", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleParallelActions(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleParallelActions_MissingTabID(t *testing.T) {
	h := New(&mockBridge{}, &config.RuntimeConfig{ActionTimeout: 5 * time.Second}, nil, nil, nil)
	body := `{"groups":[{"tabId":"","actions":[{"kind":"click"}]}]}`
	req := httptest.NewRequest("POST", "/tabs/parallel", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleParallelActions(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleParallelActions_DuplicateTabID(t *testing.T) {
	h := New(&mockBridge{}, &config.RuntimeConfig{ActionTimeout: 5 * time.Second}, nil, nil, nil)
	body := `{"groups":[{"tabId":"tab1","actions":[{"kind":"click"}]},{"tabId":"tab1","actions":[{"kind":"type"}]}]}`
	req := httptest.NewRequest("POST", "/tabs/parallel", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleParallelActions(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleParallelActions_EmptyActions(t *testing.T) {
	h := New(&mockBridge{}, &config.RuntimeConfig{ActionTimeout: 5 * time.Second}, nil, nil, nil)
	body := `{"groups":[{"tabId":"tab1","actions":[]}]}`
	req := httptest.NewRequest("POST", "/tabs/parallel", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleParallelActions(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleParallelActions_TabNotFound(t *testing.T) {
	h := New(&mockBridge{failTab: true}, &config.RuntimeConfig{ActionTimeout: 5 * time.Second}, nil, nil, nil)
	body := `{"groups":[{"tabId":"tab_noexist","actions":[{"kind":"click"}]}]}`
	req := httptest.NewRequest("POST", "/tabs/parallel", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleParallelActions(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleParallelActions_BadJSON(t *testing.T) {
	h := New(&mockBridge{}, &config.RuntimeConfig{ActionTimeout: 5 * time.Second}, nil, nil, nil)
	req := httptest.NewRequest("POST", "/tabs/parallel", bytes.NewBufferString("{bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleParallelActions(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleParallelActions_Success(t *testing.T) {
	h := New(&mockBridge{}, &config.RuntimeConfig{ActionTimeout: 5 * time.Second}, nil, nil, nil)
	body := `{"groups":[{"tabId":"tab1","actions":[{"kind":"click","selector":"#btn"}]}]}`
	req := httptest.NewRequest("POST", "/tabs/parallel", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleParallelActions(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp bridge.ParallelResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(resp.Groups))
	}
}
