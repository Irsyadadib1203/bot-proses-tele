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

// DefaultAdapter is a sample implementation of core.ProductAdapter connecting to a provider H2H API.
// New clients should customize this file according to their target provider's API documentation.
type DefaultAdapter struct {
	cfg        config.TargetConfig
	productMap map[string]string
	httpClient *http.Client
	logger     *slog.Logger
	isMockMode bool
}

// ProviderOrderRequest is the JSON payload structure expected by the provider H2H API.
type ProviderOrderRequest struct {
	RefID       string `json:"ref_id"` // Idempotency key
	ProductCode string `json:"product_code"`
	CustomerNo  string `json:"customer_no"`
	Sign        string `json:"sign,omitempty"`
}

// ProviderOrderResponse is the standard response structure returned by the provider.
type ProviderOrderResponse struct {
	Status      string `json:"status"` // "SUCCESS", "FAILED", "PENDING"
	SN          string `json:"sn"`
	ProviderRef string `json:"provider_ref"`
	Message     string `json:"message"`
	ErrorCode   string `json:"error_code,omitempty"`
}

func NewDefaultAdapter(targetCfg config.TargetConfig, productMap map[string]string, logger *slog.Logger) *DefaultAdapter {
	if logger == nil {
		logger = slog.Default()
	}

	timeout := 15 * time.Second
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

// PlaceOrder sends the order request to the target provider.
func (a *DefaultAdapter) PlaceOrder(ctx context.Context, req core.PlaceOrderRequest) (core.OrderResult, error) {
	// 1. Resolve product code mapping
	providerProductCode, ok := a.productMap[req.ProductCode]
	if !ok {
		// Fallback to given product code if no mapping is configured
		providerProductCode = req.ProductCode
	}

	// 2. If in mock mode (for local testing/demonstration), return a simulated response
	if a.isMockMode {
		return a.simulateMockOrder(ctx, req, providerProductCode)
	}

	// 3. Build real HTTP request to target provider
	payload := ProviderOrderRequest{
		RefID:       req.IdempotencyKey,
		ProductCode: providerProductCode,
		CustomerNo:  req.TargetID,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return core.OrderResult{}, fmt.Errorf("failed to marshal order payload: %w", err)
	}

	url := fmt.Sprintf("%s/order", strings.TrimRight(a.cfg.BaseURL, "/"))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.OrderResult{}, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
		httpReq.Header.Set("X-API-Key", a.cfg.APIKey)
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
			Message: fmt.Sprintf("failed to read response: %v", err),
		}, err
	}

	var providerResp ProviderOrderResponse
	if err := json.Unmarshal(respBytes, &providerResp); err != nil {
		return core.OrderResult{
			Status:  core.OrderStatusFailed,
			Message: fmt.Sprintf("invalid JSON response (HTTP %d): %s", resp.StatusCode, string(respBytes)),
		}, nil
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.ToUpper(providerResp.Status) == "SUCCESS" {
		return core.OrderResult{
			SN:          providerResp.SN,
			ProviderRef: providerResp.ProviderRef,
			Status:      core.OrderStatusSuccess,
			Message:     providerResp.Message,
		}, nil
	}

	return core.OrderResult{
		SN:          providerResp.SN,
		ProviderRef: providerResp.ProviderRef,
		Status:      core.OrderStatusFailed,
		Message:     providerResp.Message,
	}, nil
}

// CheckStatus verifies the order status on provider side using its idempotency key.
// Returns (result, supported, error).
func (a *DefaultAdapter) CheckStatus(ctx context.Context, idempotencyKey string) (core.OrderResult, bool, error) {
	if a.isMockMode {
		// Mock adapter supports check status
		return core.OrderResult{
			SN:          "MOCK-SN-RESUMED",
			ProviderRef: "MOCK-REF-" + idempotencyKey,
			Status:      core.OrderStatusSuccess,
			Message:     "Verified via mock check status",
		}, true, nil
	}

	// NOTE FOR NEW CLIENTS:
	// If the client's provider does NOT support status inquiry by ref_id, simply return:
	// return core.OrderResult{}, false, nil

	url := fmt.Sprintf("%s/order/status?ref_id=%s", strings.TrimRight(a.cfg.BaseURL, "/"), idempotencyKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return core.OrderResult{}, true, fmt.Errorf("failed to create check status request: %w", err)
	}

	if a.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
		httpReq.Header.Set("X-API-Key", a.cfg.APIKey)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return core.OrderResult{}, true, fmt.Errorf("check status request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Order was never received by provider
		return core.OrderResult{Status: "not_found"}, true, nil
	}

	var providerResp ProviderOrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&providerResp); err != nil {
		return core.OrderResult{}, true, fmt.Errorf("failed to decode check status response: %w", err)
	}

	if strings.ToUpper(providerResp.Status) == "SUCCESS" {
		return core.OrderResult{
			SN:          providerResp.SN,
			ProviderRef: providerResp.ProviderRef,
			Status:      core.OrderStatusSuccess,
			Message:     providerResp.Message,
		}, true, nil
	}

	if strings.ToUpper(providerResp.Status) == "FAILED" {
		return core.OrderResult{
			SN:          providerResp.SN,
			ProviderRef: providerResp.ProviderRef,
			Status:      core.OrderStatusFailed,
			Message:     providerResp.Message,
		}, true, nil
	}

	return core.OrderResult{
		Status:  "unknown",
		Message: providerResp.Message,
	}, true, nil
}

// simulateMockOrder simulates a realistic external API call with brief latency and sample SN generation.
func (a *DefaultAdapter) simulateMockOrder(ctx context.Context, req core.PlaceOrderRequest, providerCode string) (core.OrderResult, error) {
	// Simulate 100-300ms network latency
	select {
	case <-ctx.Done():
		return core.OrderResult{Status: core.OrderStatusFailed, Message: "context canceled"}, ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}

	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	randHex := hex.EncodeToString(randBytes)

	// Simulated serial number
	sn := fmt.Sprintf("SN-%s-%d-%s", providerCode, time.Now().Unix(), strings.ToUpper(randHex))
	providerRef := fmt.Sprintf("PRV-%s", req.IdempotencyKey)

	return core.OrderResult{
		SN:          sn,
		ProviderRef: providerRef,
		Status:      core.OrderStatusSuccess,
		Message:     "Simulated purchase successful",
	}, nil
}
