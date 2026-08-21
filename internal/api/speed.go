package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"netwatch/internal/probe"
)

// downloadPayload is incompressible pseudo-random data so proxies/browsers
// cannot silently compress the stream and inflate measured throughput.
var (
	downloadPayload     []byte
	downloadPayloadOnce sync.Once
)

func localDownloadPayload() []byte {
	downloadPayloadOnce.Do(func() {
		// 2 MiB chunks reduce syscall overhead on multi-gig paths.
		buf := make([]byte, 2*1024*1024)
		// math/rand is fine here: we only need entropy against compression, not crypto.
		r := rand.New(rand.NewSource(0x4e455457)) // NETW
		for i := range buf {
			buf[i] = byte(r.Intn(256))
		}
		downloadPayload = buf
	})
	return downloadPayload
}

// maxLocalUploadBytes caps a single upload request body. Multi-stream tests
// restart streams, so this only bounds one blob (not total test volume).
var maxLocalUploadBytes int64 = 512 * 1024 * 1024

const maxConcurrentSpeedStreams = 8

func (h *Handler) acquireSpeedSlot(w http.ResponseWriter) bool {
	select {
	case h.speedSlots <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "2")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many concurrent speed tests"})
		return false
	}
}

func (h *Handler) releaseSpeedSlot() {
	<-h.speedSlots
}

func (h *Handler) handleSpeedConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetSpeedConfig())
}

func (h *Handler) handleBroadbandStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var request probe.BroadbandServerRequest
	if r.Body != nil {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if err := decoder.Decode(&request); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
			return
		}
	}
	request.NodeID = strings.TrimSpace(request.NodeID)
	writeJSON(w, http.StatusOK, h.service.StartBroadbandTaskWithRequest(request))
}

func (h *Handler) handleBroadbandTask(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetBroadbandTask())
}

func (h *Handler) handleBroadbandCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.CancelBroadbandTask())
}

func (h *Handler) handleBroadbandCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.GetPublicBroadbandNodes())
}

func (h *Handler) handleBroadbandClientResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var result probe.BroadbandSpeedResult
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	if err := decoder.Decode(&result); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if !validClientBroadbandResult(result) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid speed result"})
		return
	}
	result.Timestamp = ""
	writeJSON(w, http.StatusOK, h.service.RecordClientBroadbandResult(result))
}

func (h *Handler) handleBroadbandHistory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetBroadbandHistory())
}

func (h *Handler) handleLocalHistory(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.service.GetLocalTransferHistory())
}

func (h *Handler) handleSpeedHistoryNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var request struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	request.Note = strings.TrimSpace(request.Note)
	if request.Kind != "broadband" && request.Kind != "local" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid history kind"})
		return
	}
	if len([]rune(request.Note)) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "note too long"})
		return
	}
	if !h.service.UpdateSpeedHistoryNote(request.Kind, request.ID, request.Note) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "history item not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": request.Note})
}

func (h *Handler) handleLocalResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var result probe.LocalTransferResult
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	if err := decoder.Decode(&result); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if !validLocalTransferResult(result) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid speed result"})
		return
	}
	writeJSON(w, http.StatusOK, h.service.RecordLocalTransferResult(result))
}

func (h *Handler) handleLocalPing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"time": time.Now().Format(time.DateTime)})
}

func (h *Handler) handleLocalDownload(w http.ResponseWriter, r *http.Request) {
	if !h.acquireSpeedSlot(w) {
		return
	}
	defer h.releaseSpeedSlot()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	// Discourage intermediate compression / transformation.
	w.Header().Set("Content-Encoding", "identity")

	payload := localDownloadPayload()
	secStr := r.URL.Query().Get("sec")
	if secStr != "" {
		sec, err := strconv.ParseFloat(secStr, 64)
		if err != nil || sec <= 0 {
			sec = 10
		}
		if sec > 60 {
			sec = 60
		}
		// Client multi-stream tests may keep a connection slightly longer than
		// the nominal window; allow a small overrun so streams do not starve.
		deadline := time.Now().Add(time.Duration((sec + 1.5) * float64(time.Second)))
		ctx := r.Context()
		flusher, canFlush := w.(http.Flusher)
		nextFlush := time.Now()
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if _, err := w.Write(payload); err != nil {
				return
			}
			// Flush aggressively so browser onprogress sees real-time bytes
			// instead of large buffered bursts that under-report mid-test rate.
			if canFlush && !time.Now().Before(nextFlush) {
				flusher.Flush()
				nextFlush = time.Now().Add(50 * time.Millisecond)
			}
		}
		if canFlush {
			flusher.Flush()
		}
		return
	}

	mb := parseMB(r.URL.Query().Get("mb"), 8)
	remaining := mb * 1024 * 1024
	ctx := r.Context()
	flusher, canFlush := w.(http.Flusher)
	for remaining > 0 {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n := len(payload)
		if remaining < n {
			n = remaining
		}
		if _, err := w.Write(payload[:n]); err != nil {
			return
		}
		remaining -= n
		if canFlush {
			flusher.Flush()
		}
	}
}

func (h *Handler) handleLocalUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !h.acquireSpeedSlot(w) {
		return
	}
	defer h.releaseSpeedSlot()
	// Read body as-is without decompression/transform so measured size matches wire intent.
	w.Header().Set("Cache-Control", "no-store")
	n, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxLocalUploadBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "payload too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"received_bytes": n, "time": time.Now().Format(time.DateTime)})
}

func validLocalTransferResult(result probe.LocalTransferResult) bool {
	for _, value := range []float64{
		result.DownloadMbps,
		result.UploadMbps,
		result.PayloadMB,
		result.DownloadMB,
		result.UploadMB,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false
		}
	}
	return result.RoundTripLatencyMS >= 0 &&
		result.RTTMinMS >= 0 &&
		result.RTTAvgMS >= 0 &&
		result.RTTMaxMS >= 0 &&
		result.JitterMS >= 0 &&
		result.DurationMS >= 0
}

func validClientBroadbandResult(result probe.BroadbandSpeedResult) bool {
	for _, value := range []float64{result.DownloadMbps, result.UploadMbps} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100000 {
			return false
		}
	}
	if result.LatencyMS < 0 || result.LatencyMS > 600000 || result.JitterMS < 0 || result.JitterMS > 600000 {
		return false
	}
	if len([]rune(result.NodeID)) > 80 || len([]rune(result.NodeName)) > 160 || len([]rune(result.NodeCategory)) > 48 {
		return false
	}
	if result.NodeID == "" {
		return false
	}
	return true
}

// clampQueryLimit parses ?limit= with a default and hard max so list/history
// endpoints share the same contract.
func clampQueryLimit(raw string, def, max int) int {
	if def <= 0 {
		def = 1
	}
	if max > 0 && def > max {
		def = max
	}
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

func parseMB(value string, fallback int) int {
	mb, err := strconv.Atoi(value)
	if err != nil || mb <= 0 {
		return fallback
	}
	if mb > 256 {
		return 256
	}
	return mb
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
