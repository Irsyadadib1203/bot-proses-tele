package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"bot-proses/internal/core"
	"bot-proses/internal/store"
)

// FFZCallbackPayload represents the webhook callback payload sent by FFZ Store API.
type FFZCallbackPayload struct {
	InvoiceNumber string          `json:"invoice_number"`
	TrxID         *string         `json:"trx_id"`
	ResponseNote  string          `json:"response_note"`
	Category      *CategoryInfo   `json:"category"`
	Product       *ProductInfo    `json:"product"`
	UserInput     *UserInputInfo  `json:"user_input"`
	Balance       int64           `json:"balance"`
	Amount        int             `json:"amount"`
	Status        string          `json:"status"` // SUCCESS | PARTIAL_SUCCESS | PENDING | PAID | REFUNDED | FAILED
	Voucher       *string         `json:"voucher"`
	CreatedAt     int64           `json:"created_at"`
}

type CategoryInfo struct {
	Title    string  `json:"title"`
	Subtitle *string `json:"subtitle"`
	Type     string  `json:"type"`
}

type ProductInfo struct {
	Name  string `json:"name"`
	Code  string `json:"code"`
	Price int    `json:"price"`
}

type UserInputInfo struct {
	UserID   string `json:"user_id"`
	ServerID string `json:"server_id"`
	Nickname string `json:"nickname"`
}

// IPRateLimiter provides thread-safe in-memory rate limiting per client IP.
type IPRateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func NewIPRateLimiter(limit int, window time.Duration) *IPRateLimiter {
	return &IPRateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (limiter *IPRateLimiter) Allow(ip string) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-limiter.window)

	// Clean old timestamps
	var validTimestamps []time.Time
	for _, t := range limiter.requests[ip] {
		if t.After(cutoff) {
			validTimestamps = append(validTimestamps, t)
		}
	}

	if len(validTimestamps) >= limiter.limit {
		limiter.requests[ip] = validTimestamps
		return false
	}

	validTimestamps = append(validTimestamps, now)
	limiter.requests[ip] = validTimestamps
	return true
}

// WebhookHandler handles HTTP POST callbacks from the external provider.
type WebhookHandler struct {
	repo         store.Repository
	orchestrator *core.Orchestrator
	apiKey       string
	logger       *slog.Logger
	rateLimiter  *IPRateLimiter
}

func NewWebhookHandler(
	repo store.Repository,
	orchestrator *core.Orchestrator,
	apiKey string,
	logger *slog.Logger,
) *WebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{
		repo:         repo,
		orchestrator: orchestrator,
		apiKey:       apiKey,
		logger:       logger,
		rateLimiter:  NewIPRateLimiter(120, time.Minute), // Allow up to 120 reqs/min per IP
	}
}

// verifySignature computes HMAC-SHA256 from the raw JSON request body and performs constant-time comparison with X-Signature header.
func (h *WebhookHandler) verifySignature(rawBody []byte, signatureHeader string) bool {
	if signatureHeader == "" || h.apiKey == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(h.apiKey))
	mac.Write(rawBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signatureHeader))
}

// extractRefID extracts the idempotency key fallback from response_note.
// Example format: "Nickname - 123456789(1234) . RefId: XX_1679528285_1234"
func extractRefID(responseNote string) string {
	idx := strings.Index(responseNote, "RefId:")
	if idx == -1 {
		return ""
	}
	ref := strings.TrimSpace(responseNote[idx+len("RefId:"):])
	if spaceIdx := strings.IndexAny(ref, " \t\n"); spaceIdx != -1 {
		ref = ref[:spaceIdx]
	}
	return ref
}

// findItemWithRetry performs retries to close race conditions where the callback arrives before DB commit completes.
func (h *WebhookHandler) findItemWithRetry(ctx context.Context, providerRef, responseNote string) *store.BatchOrderItem {
	const maxAttempts = 3
	const retryDelay = 500 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var item *store.BatchOrderItem
		if providerRef != "" {
			item, _ = h.repo.GetItemByProviderRef(ctx, providerRef)
		}
		if item == nil {
			if refID := extractRefID(responseNote); refID != "" {
				item, _ = h.repo.GetItemByIdempotencyKey(ctx, refID)
			}
		}
		if item != nil {
			return item
		}
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(retryDelay):
			}
		}
	}
	return nil
}

// HandleCallback processes incoming provider webhook notifications.
func (h *WebhookHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// 1. Revisi 3: Reject non-POST requests with 405 Method Not Allowed
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 2. IP Rate Limiting
	clientIP := extractIP(r)
	if !h.rateLimiter.Allow(clientIP) {
		h.logger.Warn("webhook rate limit exceeded", "ip", clientIP)
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	// 3. Read raw body bytes BEFORE unmarshaling to prevent key/formatting alterations
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn("failed to read webhook raw body", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// 4. Verify HMAC-SHA256 signature
	sigHeader := r.Header.Get("X-Signature")
	if !h.verifySignature(rawBody, sigHeader) {
		h.logger.Warn("invalid or missing webhook X-Signature", "ip", clientIP)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// 5. Parse JSON body
	var payload FFZCallbackPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		h.logger.Warn("failed to parse webhook json payload", "err", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// 6. Find item in database with race-condition retry and fallback RefId
	ctx := r.Context()
	item := h.findItemWithRetry(ctx, payload.InvoiceNumber, payload.ResponseNote)
	if item == nil {
		h.logger.Warn("webhook callback item not found or already completed",
			"invoice_number", payload.InvoiceNumber,
			"response_note", payload.ResponseNote,
			"status", payload.Status,
		)
		// Return 200 to prevent provider from indefinitely retrying invalid or already processed callback
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
		return
	}

	// 7. Map provider status to internal status
	statusUpper := strings.ToUpper(payload.Status)
	var mappedStatus string
	var errorMsg string

	switch statusUpper {
	case "SUCCESS", "PARTIAL_SUCCESS", "PAID":
		mappedStatus = store.ItemStatusSuccess
	case "FAILED", "REFUNDED":
		mappedStatus = store.ItemStatusFailed
		errorMsg = payload.ResponseNote
		if errorMsg == "" {
			errorMsg = fmt.Sprintf("provider status: %s", statusUpper)
		}
	case "PENDING":
		// Still pending on provider side; acknowledgment returned without updating to final status
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
		return
	default:
		mappedStatus = store.ItemStatusFailed
		errorMsg = fmt.Sprintf("unknown provider status: %s", statusUpper)
	}

	// 8. Update database item result
	invoiceNum := payload.InvoiceNumber
	if invoiceNum == "" && item.ProviderRef.Valid {
		invoiceNum = item.ProviderRef.String
	}

	sn := payload.ResponseNote
	if sn == "" {
		sn = invoiceNum
	}

	dbCtx, cancelDB := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDB()

	if err := h.repo.UpdateItemResult(dbCtx, item.ID, mappedStatus, sn, invoiceNum, errorMsg); err != nil {
		h.logger.Error("failed to update item result from webhook", "item_id", item.ID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("webhook processed order item",
		"batch_id", item.BatchID,
		"item_id", item.ID,
		"seq", item.SequenceNo,
		"status", mappedStatus,
		"invoice_number", invoiceNum,
	)

	// 9. Trigger batch completion check (finalizes batch and sends recap if all items are done)
	if h.orchestrator != nil {
		h.orchestrator.CheckAndFinalizeBatch(context.Background(), item.BatchID)
	}

	// 10. Reply 200 OK
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"received":true}`))
}

func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}