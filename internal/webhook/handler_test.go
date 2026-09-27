package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"bot-proses/internal/core"
	"bot-proses/internal/store"
)

// mockWebhookRepo implements store.Repository for webhook tests
type mockWebhookRepo struct {
	mu            sync.Mutex
	batch         *store.BatchOrder
	items         map[int64]*store.BatchOrderItem
	updateResults []string
}

func newMockWebhookRepo(batch *store.BatchOrder, items []*store.BatchOrderItem) *mockWebhookRepo {
	m := &mockWebhookRepo{
		batch: batch,
		items: make(map[int64]*store.BatchOrderItem),
	}
	for _, it := range items {
		m.items[it.ID] = it
	}
	return m
}

func (m *mockWebhookRepo) CreateBatch(ctx context.Context, batch *store.BatchOrder, items []*store.BatchOrderItem) (int64, error) {
	return 1, nil
}
func (m *mockWebhookRepo) GetBatch(ctx context.Context, batchID int64) (*store.BatchOrder, error) {
	return m.batch, nil
}
func (m *mockWebhookRepo) GetProcessingBatches(ctx context.Context) ([]*store.BatchOrder, error) {
	return []*store.BatchOrder{m.batch}, nil
}
func (m *mockWebhookRepo) GetBatchItems(ctx context.Context, batchID int64) ([]*store.BatchOrderItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []*store.BatchOrderItem
	for _, it := range m.items {
		res = append(res, it)
	}
	return res, nil
}
func (m *mockWebhookRepo) GetPendingItems(ctx context.Context, batchID int64) ([]*store.BatchOrderItem, error) {
	return nil, nil
}
func (m *mockWebhookRepo) GetInProgressItems(ctx context.Context, batchID int64) ([]*store.BatchOrderItem, error) {
	return nil, nil
}
func (m *mockWebhookRepo) UpdateItemStatus(ctx context.Context, itemID int64, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.items[itemID]; ok {
		it.Status = status
	}
	return nil
}
func (m *mockWebhookRepo) UpdateItemProviderRef(ctx context.Context, itemID int64, providerRef string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.items[itemID]; ok {
		it.ProviderRef = sql.NullString{String: providerRef, Valid: providerRef != ""}
	}
	return nil
}
func (m *mockWebhookRepo) GetItemByProviderRef(ctx context.Context, providerRef string) (*store.BatchOrderItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.ProviderRef.Valid && it.ProviderRef.String == providerRef && it.Status == store.ItemStatusInProgress {
			return it, nil
		}
	}
	return nil, nil
}
func (m *mockWebhookRepo) GetItemByIdempotencyKey(ctx context.Context, idempotencyKey string) (*store.BatchOrderItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.IdempotencyKey == idempotencyKey && it.Status == store.ItemStatusInProgress {
			return it, nil
		}
	}
	return nil, nil
}
func (m *mockWebhookRepo) UpdateItemResult(ctx context.Context, itemID int64, status, sn, providerRef, errorMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.items[itemID]; ok {
		it.Status = status
		if sn != "" {
			it.SN = sql.NullString{String: sn, Valid: true}
		}
		if providerRef != "" {
			it.ProviderRef = sql.NullString{String: providerRef, Valid: true}
		}
		if errorMsg != "" {
			it.ErrorMessage = sql.NullString{String: errorMsg, Valid: true}
		}
		m.updateResults = append(m.updateResults, fmt.Sprintf("%d:%s:%s", itemID, status, providerRef))
	}
	return nil
}
func (m *mockWebhookRepo) UpdateBatchStatus(ctx context.Context, batchID int64, status string, successCount, failedCount, manualReviewCount int, completedAt *time.Time) error {
	return nil
}
func (m *mockWebhookRepo) CheckAndCompleteBatch(ctx context.Context, batchID int64) (bool, *store.BatchOrder, error) {
	return false, m.batch, nil
}

func computeSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	apiKey := "my_super_secret_api_key_123"
	handler := NewWebhookHandler(nil, nil, apiKey, nil)

	body := []byte(`{"invoice_number":"INV-001","status":"SUCCESS"}`)
	validSig := computeSignature(body, apiKey)

	// 1. Valid signature
	if !handler.verifySignature(body, validSig) {
		t.Errorf("expected valid signature to pass verification")
	}

	// 2. Invalid signature
	if handler.verifySignature(body, "invalid_signature_hex") {
		t.Errorf("expected invalid signature to fail")
	}

	// 3. Empty signature
	if handler.verifySignature(body, "") {
		t.Errorf("expected empty signature to fail")
	}

	// 4. Tampered body
	tamperedBody := []byte(`{"invoice_number":"INV-001","status":"FAILED"}`)
	if handler.verifySignature(tamperedBody, validSig) {
		t.Errorf("expected tampered body to fail verification")
	}
}

func TestVerifySignature_RawBodyVsRemarshal(t *testing.T) {
	apiKey := "secret_key_abc"
	handler := NewWebhookHandler(nil, nil, apiKey, nil)

	// Raw JSON formatted with unusual spacing and specific key order
	rawBody := []byte(`{  "status":   "SUCCESS"  ,  "invoice_number": "INV-RAW-999" , "created_at":1729926123 }`)
	validSig := computeSignature(rawBody, apiKey)

	// Verifying with the original raw body MUST succeed
	if !handler.verifySignature(rawBody, validSig) {
		t.Fatalf("expected raw body verification to succeed")
	}

	// If remarshaled via a Go struct, the bytes will differ and must NOT be used for signature checking
	var payload FFZCallbackPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	remarshaledBytes, _ := json.Marshal(payload)

	// The remarshaled bytes are structurally equivalent but byte-wise different
	if bytes.Equal(rawBody, remarshaledBytes) {
		t.Fatalf("test precondition failed: raw bytes and remarshaled bytes should differ")
	}

	// Signature verification with remarshaled bytes would fail
	if handler.verifySignature(remarshaledBytes, validSig) {
		t.Errorf("remarshaled bytes should fail against signature generated from raw body")
	}
}

func TestHandleCallback_MethodNotAllowed(t *testing.T) {
	handler := NewWebhookHandler(nil, nil, "apikey", nil)

	req := httptest.NewRequest(http.MethodGet, "/webhook/callback", nil)
	rec := httptest.NewRecorder()

	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected HTTP 405 Method Not Allowed, got %d", rec.Code)
	}
}

func TestHandleCallback_UnauthorizedSignature(t *testing.T) {
	batch := &store.BatchOrder{ID: 1}
	item := &store.BatchOrderItem{
		ID:          1,
		BatchID:     1,
		ProviderRef: sql.NullString{String: "INV-100", Valid: true},
		Status:      store.ItemStatusInProgress,
	}
	repo := newMockWebhookRepo(batch, []*store.BatchOrderItem{item})

	apiKey := "target_api_key_123"
	handler := NewWebhookHandler(repo, nil, apiKey, nil)

	body := []byte(`{"invoice_number":"INV-100","status":"SUCCESS"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/callback", bytes.NewReader(body))
	req.Header.Set("X-Signature", "wrong_signature")
	rec := httptest.NewRecorder()

	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Ensure database was NOT modified
	if len(repo.updateResults) != 0 {
		t.Errorf("expected 0 database updates on unauthorized request, got %d", len(repo.updateResults))
	}
}

func TestHandleCallback_InvoiceNotFound(t *testing.T) {
	batch := &store.BatchOrder{ID: 1}
	item := &store.BatchOrderItem{
		ID:          1,
		BatchID:     1,
		ProviderRef: sql.NullString{String: "INV-100", Valid: true},
		Status:      store.ItemStatusInProgress,
	}
	repo := newMockWebhookRepo(batch, []*store.BatchOrderItem{item})

	apiKey := "target_api_key_123"
	handler := NewWebhookHandler(repo, nil, apiKey, nil)

	body := []byte(`{"invoice_number":"INV-NONEXISTENT","status":"SUCCESS","response_note":"No ref"}`)
	sig := computeSignature(body, apiKey)

	req := httptest.NewRequest(http.MethodPost, "/webhook/callback", bytes.NewReader(body))
	req.Header.Set("X-Signature", sig)
	rec := httptest.NewRecorder()

	handler.HandleCallback(rec, req)

	// Idempotent: must return 200 OK to prevent provider retry storms
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// No data changed
	if len(repo.updateResults) != 0 {
		t.Errorf("expected no DB updates for unknown invoice, got %d", len(repo.updateResults))
	}
}

func TestHandleCallback_Success(t *testing.T) {
	batch := &store.BatchOrder{ID: 1}
	item := &store.BatchOrderItem{
		ID:          10,
		BatchID:     1,
		SequenceNo:  1,
		ProviderRef: sql.NullString{String: "APIKUY_XX_1679528285_4321", Valid: true},
		Status:      store.ItemStatusInProgress,
	}
	repo := newMockWebhookRepo(batch, []*store.BatchOrderItem{item})

	apiKey := "target_api_key_123"
	orch := core.NewOrchestrator(repo, nil, 1, nil, nil)
	defer orch.Stop(1 * time.Second)

	handler := NewWebhookHandler(repo, orch, apiKey, nil)

	body := []byte(`{
		"invoice_number": "APIKUY_XX_1679528285_4321",
		"trx_id": null,
		"response_note": "Nickname - 123456789(1234) . RefId: 1-1",
		"status": "SUCCESS",
		"amount": 1511,
		"created_at": 1729926123
	}`)
	sig := computeSignature(body, apiKey)

	req := httptest.NewRequest(http.MethodPost, "/webhook/callback", bytes.NewReader(body))
	req.Header.Set("X-Signature", sig)
	rec := httptest.NewRecorder()

	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	repo.mu.Lock()
	updatedItem := repo.items[10]
	repo.mu.Unlock()

	if updatedItem.Status != store.ItemStatusSuccess {
		t.Errorf("item status = %s, want %s", updatedItem.Status, store.ItemStatusSuccess)
	}
	expectedSN := "Nickname - 123456789(1234) . RefId: 1-1"
	if updatedItem.SN.String != expectedSN {
		t.Errorf("item SN = %s, want %s", updatedItem.SN.String, expectedSN)
	}
	if updatedItem.ProviderRef.String != "APIKUY_XX_1679528285_4321" {
		t.Errorf("item ProviderRef = %s, want APIKUY_XX_1679528285_4321", updatedItem.ProviderRef.String)
	}
}

func TestHandleCallback_FailedAndRefunded(t *testing.T) {
	batch := &store.BatchOrder{ID: 1}
	item := &store.BatchOrderItem{
		ID:          20,
		BatchID:     1,
		SequenceNo:  1,
		ProviderRef: sql.NullString{String: "APIKUY_FAIL_123", Valid: true},
		Status:      store.ItemStatusInProgress,
	}
	repo := newMockWebhookRepo(batch, []*store.BatchOrderItem{item})

	apiKey := "target_api_key_123"
	handler := NewWebhookHandler(repo, nil, apiKey, nil)

	body := []byte(`{
		"invoice_number": "APIKUY_FAIL_123",
		"response_note": "Account ID not found",
		"status": "FAILED"
	}`)
	sig := computeSignature(body, apiKey)

	req := httptest.NewRequest(http.MethodPost, "/webhook/callback", bytes.NewReader(body))
	req.Header.Set("X-Signature", sig)
	rec := httptest.NewRecorder()

	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	repo.mu.Lock()
	updatedItem := repo.items[20]
	repo.mu.Unlock()

	if updatedItem.Status != store.ItemStatusFailed {
		t.Errorf("item status = %s, want %s", updatedItem.Status, store.ItemStatusFailed)
	}
	if updatedItem.ErrorMessage.String != "Account ID not found" {
		t.Errorf("item error_message = %s, want 'Account ID not found'", updatedItem.ErrorMessage.String)
	}
}

func TestHandleCallback_DuplicateCallback(t *testing.T) {
	batch := &store.BatchOrder{ID: 1}
	item := &store.BatchOrderItem{
		ID:          30,
		BatchID:     1,
		SequenceNo:  1,
		ProviderRef: sql.NullString{String: "INV-DUP-1", Valid: true},
		Status:      store.ItemStatusInProgress,
	}
	repo := newMockWebhookRepo(batch, []*store.BatchOrderItem{item})

	apiKey := "target_api_key_123"
	handler := NewWebhookHandler(repo, nil, apiKey, nil)

	body := []byte(`{"invoice_number":"INV-DUP-1","status":"SUCCESS","response_note":"Success"}`)
	sig := computeSignature(body, apiKey)

	// First callback: transitions to success
	req1 := httptest.NewRequest(http.MethodPost, "/webhook/callback", bytes.NewReader(body))
	req1.Header.Set("X-Signature", sig)
	rec1 := httptest.NewRecorder()
	handler.HandleCallback(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("first callback code = %d, want 200", rec1.Code)
	}

	repo.mu.Lock()
	firstUpdateCount := len(repo.updateResults)
	repo.mu.Unlock()

	if firstUpdateCount != 1 {
		t.Fatalf("expected 1 update after first callback, got %d", firstUpdateCount)
	}

	// Second callback (retry from provider): item is now status 'success' (no longer 'in_progress')
	req2 := httptest.NewRequest(http.MethodPost, "/webhook/callback", bytes.NewReader(body))
	req2.Header.Set("X-Signature", sig)
	rec2 := httptest.NewRecorder()
	handler.HandleCallback(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("second callback code = %d, want 200", rec2.Code)
	}

	repo.mu.Lock()
	secondUpdateCount := len(repo.updateResults)
	repo.mu.Unlock()

	if secondUpdateCount != 1 {
		t.Errorf("second callback should NOT trigger additional DB updates; count = %d, want 1", secondUpdateCount)
	}
}

func TestHandleCallback_FallbackRefId(t *testing.T) {
	batch := &store.BatchOrder{ID: 1}
	// Item in DB does NOT have provider_ref populated yet, but has IdempotencyKey "1-5"
	item := &store.BatchOrderItem{
		ID:             50,
		BatchID:        1,
		SequenceNo:     5,
		IdempotencyKey: "1-5",
		ProviderRef:    sql.NullString{Valid: false},
		Status:         store.ItemStatusInProgress,
	}
	repo := newMockWebhookRepo(batch, []*store.BatchOrderItem{item})

	apiKey := "target_api_key_123"
	handler := NewWebhookHandler(repo, nil, apiKey, nil)

	body := []byte(`{
		"invoice_number": "INV-NEW-UNSAVED",
		"response_note": "Nickname - 123456(12) . RefId: 1-5",
		"status": "SUCCESS"
	}`)
	sig := computeSignature(body, apiKey)

	req := httptest.NewRequest(http.MethodPost, "/webhook/callback", bytes.NewReader(body))
	req.Header.Set("X-Signature", sig)
	rec := httptest.NewRecorder()

	handler.HandleCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	repo.mu.Lock()
	updatedItem := repo.items[50]
	repo.mu.Unlock()

	if updatedItem.Status != store.ItemStatusSuccess {
		t.Errorf("item status = %s, want %s via fallback RefId", updatedItem.Status, store.ItemStatusSuccess)
	}
	if updatedItem.ProviderRef.String != "INV-NEW-UNSAVED" {
		t.Errorf("item provider_ref = %s, want INV-NEW-UNSAVED", updatedItem.ProviderRef.String)
	}
}

func TestHandleCallback_RateLimiting(t *testing.T) {
	apiKey := "secret"
	handler := NewWebhookHandler(nil, nil, apiKey, nil)
	// Set strict rate limiter: max 2 requests
	handler.rateLimiter = NewIPRateLimiter(2, time.Minute)

	body := []byte(`{"status":"PENDING"}`)
	sig := computeSignature(body, apiKey)

	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhook/callback", bytes.NewReader(body))
		req.Header.Set("X-Signature", sig)
		req.RemoteAddr = "192.168.1.100:12345"
		rec := httptest.NewRecorder()

		handler.HandleCallback(rec, req)

		if i <= 2 {
			if rec.Code == http.StatusTooManyRequests {
				t.Errorf("request %d should be allowed, got 429", i)
			}
		} else {
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("request %d should be rate limited with 429, got %d", i, rec.Code)
			}
		}
	}
}