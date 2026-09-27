package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"bot-proses/config"
	"bot-proses/internal/core"
)

// DefaultAdapter is the concrete adapter implementation for FFZ Store API (https://api.ffzstore.com).
type DefaultAdapter struct {
	cfg        config.TargetConfig
	productMap map[string]string
	httpClient *http.Client
	logger     *slog.Logger
	isMockMode bool
}

// FFZOrderRequest is the JSON payload required by FFZ Store API.
type FFZOrderRequest struct {
	ProductCode string `json:"product_code"`
	UserID      string `json:"user_id"`
	ServerID    string `json:"server_id,omitempty"`
	TrxID       string `json:"trx_id"`                 // Deterministic Idempotency Key
	CallbackURL string `json:"callback_url,omitempty"` // Optional webhook callback
}

// FFZOrderResponse represents the response structure from FFZ Store API.
type FFZOrderResponse struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	Data       *struct {
		InvoiceNumber string `json:"invoice_number"`
		TrxID         string `json:"trx_id"`
		ResponseNote  string `json:"response_note"`
		Status        string `json:"status"` // SUCCESS | PARTIAL_SUCCESS | PENDING | PAID | REFUNDED | FAILED
		Amount        int    `json:"amount"`
		Product       struct {
			Name  string `json:"name"`
			Code  string `json:"code"`
			Price int    `json:"price"`
		} `json:"product"`
		UserInput struct {
			UserID   string `json:"user_id"`
			ServerID string `json:"server_id"`
			Nickname string `json:"nickname"`
		} `json:"user_input"`
		CreatedAt int64 `json:"created_at"`
	} `json:"data"`
}

func NewDefaultAdapter(targetCfg config.TargetConfig, productMap map[string]string, logger *slog.Logger) *DefaultAdapter {
	if logger == nil {
		logger = slog.Default()
	}

	timeout := 20 * time.Second
	if targetCfg.TimeoutMs > 0 {
		timeout = time.Duration(targetCfg.TimeoutMs) * time.Millisecond
	}

	isMock := targetCfg.BaseURL == "" ||
		strings.Contains(targetCfg.BaseURL, "example-provider.com") ||
		strings.Contains(targetCfg.BaseURL, "mock")

	return &DefaultAdapter{
		cfg:        targetCfg,
		productMap: productMap,
		httpClient: &http.Client{Timeout: timeout},
		logger:     logger,
		isMockMode: isMock,
	}
}

// PlaceOrder sends the order request to FFZ Store API.
func (a *DefaultAdapter) PlaceOrder(ctx context.Context, req core.PlaceOrderRequest) (core.OrderResult, error) {
	// 1. Resolve product code mapping (e.g. ML5 -> S1_1187)
	providerProductCode, ok := a.productMap[req.ProductCode]
	if !ok {
		providerProductCode = req.ProductCode
	}

	// 2. Parse User ID and Server ID from TargetID
	// Supports: "123456789(1234)", "123456789|1234", "123456789 1234", or "123456789"
	userID, serverID := parseUserIDAndServerID(req.TargetID)

	// 3. Fallback to mock simulation if configured for testing
	if a.isMockMode {
		return a.simulateMockOrder(ctx, req, providerProductCode, userID, serverID)
	}

	// 4. Build JSON Request Payload for FFZ Store
	payload := FFZOrderRequest{
		ProductCode: providerProductCode,
		UserID:      userID,
		ServerID:    serverID,
		TrxID:       req.IdempotencyKey, // Deterministic Idempotency Key (<batch_id>-<sequence_no>)
		CallbackURL: a.cfg.CallbackURL,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return core.OrderResult{
			Status:  core.OrderStatusFailed,
			Message: fmt.Sprintf("failed to marshal order request: %v", err),
		}, fmt.Errorf("failed to marshal order payload: %w", err)
	}

	url := fmt.Sprintf("%s/order", strings.TrimRight(a.cfg.BaseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.OrderResult{
			Status:  core.OrderStatusFailed,
			Message: fmt.Sprintf("failed to create http request: %v", err),
		}, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", a.cfg.APIKey)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return core.OrderResult{
			Status:  core.OrderStatusFailed,
			Message: fmt.Sprintf("HTTP request error: %v", err),
		}, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.OrderResult{
			Status:  core.OrderStatusFailed,
			Message: fmt.Sprintf("failed to read response body: %v", err),
		}, err
	}

	var ffzResp FFZOrderResponse
	if err := json.Unmarshal(respBytes, &ffzResp); err != nil {
		return core.OrderResult{
			Status:  core.OrderStatusFailed,
			Message: fmt.Sprintf("invalid JSON response (HTTP %d): %s", resp.StatusCode, string(respBytes)),
		}, nil
	}

	if ffzResp.Data != nil {
		statusUpper := strings.ToUpper(ffzResp.Data.Status)
		if (resp.StatusCode == 200 || ffzResp.StatusCode == 200) &&
			(statusUpper == "SUCCESS" || statusUpper == "PAID" || statusUpper == "PARTIAL_SUCCESS") {
			return core.OrderResult{
				SN:          ffzResp.Data.InvoiceNumber,
				ProviderRef: ffzResp.Data.InvoiceNumber,
				Status:      core.OrderStatusSuccess,
				Message:     ffzResp.Data.ResponseNote,
			}, nil
		}

		if statusUpper == "PENDING" {
			return core.OrderResult{
				SN:          ffzResp.Data.InvoiceNumber,
				ProviderRef: ffzResp.Data.InvoiceNumber,
				Status:      core.OrderStatusPending,
				Message:     ffzResp.Data.ResponseNote,
			}, nil
		}

		return core.OrderResult{
			SN:          ffzResp.Data.InvoiceNumber,
			ProviderRef: ffzResp.Data.InvoiceNumber,
			Status:      core.OrderStatusFailed,
			Message:     fmt.Sprintf("[%s] %s", ffzResp.Data.Status, ffzResp.Data.ResponseNote),
		}, nil
	}

	// Response without data field
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.EqualFold(ffzResp.Message, "success") {
		return core.OrderResult{
			Status:  core.OrderStatusSuccess,
			Message: ffzResp.Message,
		}, nil
	}

	return core.OrderResult{
		Status:  core.OrderStatusFailed,
		Message: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, ffzResp.Message),
	}, nil
}

// CheckStatus verifies the real status of an order on FFZ Store API using trx_id / idempotency key.
func (a *DefaultAdapter) CheckStatus(ctx context.Context, idempotencyKey string) (core.OrderResult, bool, error) {
	if a.isMockMode {
		return core.OrderResult{
			SN:          "MOCK-INV-" + idempotencyKey,
			ProviderRef: "MOCK-REF-" + idempotencyKey,
			Status:      core.OrderStatusSuccess,
			Message:     "Verified via mock check status",
		}, true, nil
	}

	// Query status endpoint on FFZ Store API
	url := fmt.Sprintf("%s/order/status?trx_id=%s", strings.TrimRight(a.cfg.BaseURL, "/"), idempotencyKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return core.OrderResult{}, true, fmt.Errorf("failed to create check status request: %w", err)
	}

	if a.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", a.cfg.APIKey)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return core.OrderResult{}, true, fmt.Errorf("check status request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return core.OrderResult{Status: "not_found"}, true, nil
	}

	var ffzResp FFZOrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&ffzResp); err != nil {
		return core.OrderResult{}, true, fmt.Errorf("failed to decode check status response: %w", err)
	}

	if ffzResp.Data != nil {
		statusUpper := strings.ToUpper(ffzResp.Data.Status)
		if statusUpper == "SUCCESS" || statusUpper == "PAID" || statusUpper == "PARTIAL_SUCCESS" {
			return core.OrderResult{
				SN:          ffzResp.Data.InvoiceNumber,
				ProviderRef: ffzResp.Data.InvoiceNumber,
				Status:      core.OrderStatusSuccess,
				Message:     ffzResp.Data.ResponseNote,
			}, true, nil
		}

		if statusUpper == "FAILED" || statusUpper == "REFUNDED" {
			return core.OrderResult{
				SN:          ffzResp.Data.InvoiceNumber,
				ProviderRef: ffzResp.Data.InvoiceNumber,
				Status:      core.OrderStatusFailed,
				Message:     ffzResp.Data.ResponseNote,
			}, true, nil
		}

		if statusUpper == "PENDING" || statusUpper == "PROCESSING" {
			return core.OrderResult{
				SN:          ffzResp.Data.InvoiceNumber,
				ProviderRef: ffzResp.Data.InvoiceNumber,
				Status:      core.OrderStatusPending,
				Message:     ffzResp.Data.ResponseNote,
			}, true, nil
		}
	}

	return core.OrderResult{
		Status:  "unknown",
		Message: ffzResp.Message,
	}, true, nil
}

// parseUserIDAndServerID extracts user_id and server_id from various input formats.
func parseUserIDAndServerID(target string) (string, string) {
	target = strings.TrimSpace(target)

	// Format: "123456789(1234)"
	if idx := strings.Index(target, "("); idx != -1 {
		u := target[:idx]
		s := strings.TrimRight(target[idx+1:], ")")
		return strings.TrimSpace(u), strings.TrimSpace(s)
	}

	// Format: "123456789|1234", "123456789/1234", "123456789 1234"
	delims := []string{"|", "/", " "}
	for _, d := range delims {
		if strings.Contains(target, d) {
			parts := strings.SplitN(target, d, 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			}
		}
	}

	// Single target ID (e.g. Free Fire)
	return target, ""
}

// simulateMockOrder produces realistic simulated responses for development/offline testing.
func (a *DefaultAdapter) simulateMockOrder(ctx context.Context, req core.PlaceOrderRequest, providerCode, userID, serverID string) (core.OrderResult, error) {
	select {
	case <-ctx.Done():
		return core.OrderResult{Status: core.OrderStatusFailed, Message: "context canceled"}, ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}

	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	randHex := hex.EncodeToString(randBytes)

	invoice := fmt.Sprintf("FFZ_%s_%d_%s", providerCode, time.Now().Unix(), strings.ToUpper(randHex))
	note := fmt.Sprintf("Nickname - %s(%s) . TrxId: %s", userID, serverID, req.IdempotencyKey)

	return core.OrderResult{
		SN:          invoice,
		ProviderRef: invoice,
		Status:      core.OrderStatusSuccess,
		Message:     note,
	}, nil
}
