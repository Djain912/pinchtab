package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pinchtab/pinchtab/internal/bridge/observe"
	"github.com/pinchtab/pinchtab/internal/fileout"
	"github.com/pinchtab/pinchtab/internal/heapsnap"
	"github.com/pinchtab/pinchtab/internal/httpx"
	"github.com/pinchtab/pinchtab/internal/routes"
)

const (
	memorySnapshotTimeout      = 2 * time.Minute
	memorySnapshotTooLargeCode = "memory_snapshot_too_large"
	memorySnapshotNotFoundCode = "memory_snapshot_not_found"
	memorySnapshotInvalidCode  = "memory_snapshot_invalid"
	memorySnapshotMaxSetting   = "security.memorySnapshotMaxBytes"
)

type memoryUsageResponse struct {
	TabID string `json:"tabId"`
	*observe.HeapUsage
}

type memorySnapshotResponse struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	NodeCount  int    `json:"nodeCount"`
	DurationMs int64  `json:"durationMs"`
	TabID      string `json:"tabId"`
}

type memorySummaryResponse struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Top  int    `json:"top"`
	heapsnap.Summary
}

func (h *Handlers) memorySnapshotDir() string {
	return heapsnap.Dir(h.Config.StateDir)
}

func (h *Handlers) HandleMemory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tabID := q.Get("tabId")
	gc := false
	if raw := strings.TrimSpace(q.Get("gc")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			httpx.ErrorCode(w, 400, "bad_gc", fmt.Sprintf("gc must be true or false, got %q", raw), false, nil)
			return
		}
		gc = parsed
	}

	if !h.ensureBrowserOrRespond(w, h.Config) {
		return
	}
	h.recordReadRequest(r, "memory", tabID)

	resolvedTabID, tCtx, cancel, ok := h.resolveReadContext(w, r, tabID, h.Config.ActionTimeout)
	if !ok {
		return
	}
	defer h.armIdleLifecycle(resolvedTabID)
	defer cancel()

	usage, err := observe.ReadHeapUsage(tCtx, gc)
	if err != nil {
		httpx.Error(w, 500, fmt.Errorf("read heap usage: %w", err))
		return
	}
	httpx.JSON(w, 200, memoryUsageResponse{TabID: resolvedTabID, HeapUsage: usage})
}

func (h *Handlers) HandleTabMemory(w http.ResponseWriter, r *http.Request) {
	h.withPathTabID(w, r, h.HandleMemory)
}

func (h *Handlers) HandleMemorySnapshot(w http.ResponseWriter, r *http.Request) {
	if !h.memoryEnabled() {
		h.writeCapabilityDisabled(w, routes.CapMemory)
		return
	}

	var req struct {
		TabID string `json:"tabId"`
	}
	if err := httpx.DecodeOptionalJSONBody(w, r, 0, &req); err != nil {
		httpx.Error(w, httpx.StatusForJSONDecodeError(err), err)
		return
	}

	if !h.ensureBrowserOrRespond(w, h.Config) {
		return
	}
	h.recordReadRequest(r, "memory-snapshot", req.TabID)

	resolvedTabID, tCtx, cancel, ok := h.resolveReadContext(w, r, req.TabID, memorySnapshotTimeout)
	if !ok {
		return
	}
	defer h.armIdleLifecycle(resolvedTabID)
	defer cancel()

	dir := h.memorySnapshotDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		httpx.Error(w, 500, fmt.Errorf("create heap snapshot dir: %w", err))
		return
	}
	path, err := fileout.ReserveUnique(dir, heapsnap.IDPrefix+time.Now().Format("20060102_150405"), heapsnap.Ext)
	if err != nil {
		httpx.Error(w, 500, fmt.Errorf("reserve heap snapshot path: %w", err))
		return
	}
	kept := false
	defer func() {
		if !kept {
			_ = os.Remove(path)
		}
	}()

	maxBytes := h.Config.EffectiveMemorySnapshotMaxBytes()
	start := time.Now()
	written, err := streamHeapSnapshot(tCtx, path, int64(maxBytes))
	if err != nil {
		if errors.Is(err, heapsnap.ErrTooLarge) {
			httpx.ErrorCode(w, http.StatusRequestEntityTooLarge, memorySnapshotTooLargeCode,
				fmt.Sprintf("heap snapshot exceeded %d bytes and was discarded; raise %s to allow larger snapshots", maxBytes, memorySnapshotMaxSetting),
				false, map[string]any{"maxBytes": maxBytes})
			return
		}
		httpx.Error(w, 500, fmt.Errorf("take heap snapshot: %w", err))
		return
	}
	duration := time.Since(start)

	header, err := readSnapshotHeader(path)
	if err != nil {
		httpx.ErrorCode(w, 500, memorySnapshotInvalidCode, err.Error(), false, nil)
		return
	}

	kept = true
	httpx.JSON(w, 200, memorySnapshotResponse{
		ID:         heapsnap.IDFromPath(path),
		Path:       path,
		Bytes:      written,
		NodeCount:  header.NodeCount,
		DurationMs: duration.Milliseconds(),
		TabID:      resolvedTabID,
	})
}

func (h *Handlers) HandleTabMemorySnapshot(w http.ResponseWriter, r *http.Request) {
	h.withPathTabIDBody(w, r, h.HandleMemorySnapshot)
}

func streamHeapSnapshot(ctx context.Context, path string, maxBytes int64) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return 0, fmt.Errorf("open heap snapshot: %w", err)
	}
	sink := heapsnap.NewSink(f, maxBytes)
	err = observe.TakeHeapSnapshot(ctx, sink.WriteChunk)
	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("close heap snapshot: %w", closeErr)
	}
	return sink.Bytes(), err
}

func readSnapshotHeader(path string) (*heapsnap.Header, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open heap snapshot: %w", err)
	}
	defer func() { _ = f.Close() }()
	return heapsnap.ReadHeader(f)
}

func (h *Handlers) HandleMemorySnapshotSummary(w http.ResponseWriter, r *http.Request) {
	if !h.memoryEnabled() {
		h.writeCapabilityDisabled(w, routes.CapMemory)
		return
	}

	id := strings.TrimSpace(r.PathValue("snapshotId"))
	path, err := heapsnap.PathForID(h.memorySnapshotDir(), id)
	if err != nil {
		httpx.ErrorCode(w, 400, "bad_snapshot_id", err.Error(), false, nil)
		return
	}

	top := heapsnap.DefaultTop
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			httpx.ErrorCode(w, 400, "bad_top", fmt.Sprintf("top must be a positive integer, got %q", raw), false, nil)
			return
		}
		top = heapsnap.ClampTop(n)
	}

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			httpx.ErrorCode(w, 404, memorySnapshotNotFoundCode, fmt.Sprintf("no heap snapshot with id %q", id), false, nil)
			return
		}
		httpx.Error(w, 500, fmt.Errorf("stat heap snapshot: %w", err))
		return
	}

	summary, err := heapsnap.SummarizeFile(path, top)
	if err != nil {
		var parseErr *heapsnap.ParseError
		if errors.As(err, &parseErr) {
			httpx.ErrorCode(w, 422, memorySnapshotInvalidCode, err.Error(), false, map[string]any{"section": parseErr.Section})
			return
		}
		httpx.Error(w, 500, fmt.Errorf("summarize heap snapshot: %w", err))
		return
	}
	httpx.JSON(w, 200, memorySummaryResponse{ID: id, Path: path, Top: top, Summary: summary})
}
