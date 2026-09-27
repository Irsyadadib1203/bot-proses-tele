package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bot-proses/config"
	"bot-proses/internal/core"
)

func TestParseUserIDAndServerID(t *testing.T) {
	tests := []struct {
		input        string
		wantUserID   string
		wantServerID string
	}{
		{"123456789(1234)", "123456789", "1234"},
		{"123456789|1234", "123456789", "1234"},
		{"123456789/1234", "123456789", "1234"},
		{"123456789 1234", "123456789", "1234"},
		{"987654321", "987654321", ""},
	}

	for _, tt := range tests {
		u, s := parseUserIDAndServerID(tt.input)
		if u != tt.wantUserID || s != tt.wantServerID {
			t.Errorf("parseUserIDAndServerID(%q) = (%q, %q), want (%q, %q)", tt.input, u, s, tt.wantUserID, tt.wantServerID)
		}
	}
}

func TestFFZStorePlaceOrderSuccess(t *testing.T) {
	// Mock HTTP Server simulating FFZ Store API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/order" {
			http.NotFound(w, r)
			return
		}

		var req FFZOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.ProductCode != "S1_1187" || req.UserID != "123456789" || req.ServerID != "1234" {
			http.Error(w, "invalid request data", http.StatusBadRequest)
			return
		}

		if req.TrxID != "10-1" {
			http.Error(w, "missing trx_id", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"statusCode": 200,
			"message":    "success",
			"data": map[string]interface{}{
				"invoice_number": "APIKUY_XX_1679528285_4321",
				"trx_id":         req.TrxID,
				"response_note":  "Nickname - 123456789(1234) . RefId: XX_1679528285_1234",
				"status":         "SUCCESS",
				"amount":         1511,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	targetCfg := config.TargetConfig{
		BaseURL:   server.URL,
		APIKey:    "test-key",
		TimeoutMs: 5000,
	}

	productMap := map[string]string{
		"ML5": "S1_1187",
	}

	adapter := NewDefaultAdapter(targetCfg, productMap, nil)

	res, err := adapter.PlaceOrder(context.Background(), core.PlaceOrderRequest{
		ProductCode:    "ML5",
		TargetID:       "123456789(1234)",
		IdempotencyKey: "10-1",
	})

	if err != nil {
		t.Fatalf("PlaceOrder error: %v", err)
	}

	if res.Status != core.OrderStatusSuccess {
		t.Errorf("status = %s, want %s", res.Status, core.OrderStatusSuccess)
	}

	if res.SN != "APIKUY_XX_1679528285_4321" {
		t.Errorf("SN = %s, want APIKUY_XX_1679528285_4321", res.SN)
	}
}

func TestFFZStorePlaceOrderPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"statusCode": 200,
			"message":    "Order queued for processing",
			"data": map[string]interface{}{
				"invoice_number": "APIKUY_PENDING_9999",
				"trx_id":         "10-2",
				"response_note":  "Nickname - 123456789(1234) . RefId: 10-2",
				"status":         "PENDING",
				"amount":         1511,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	targetCfg := config.TargetConfig{
		BaseURL:   server.URL,
		APIKey:    "test-key",
		TimeoutMs: 5000,
	}

	adapter := NewDefaultAdapter(targetCfg, nil, nil)

	res, err := adapter.PlaceOrder(context.Background(), core.PlaceOrderRequest{
		ProductCode:    "ML5",
		TargetID:       "123456789(1234)",
		IdempotencyKey: "10-2",
	})

	if err != nil {
		t.Fatalf("PlaceOrder error: %v", err)
	}

	if res.Status != core.OrderStatusPending {
		t.Errorf("status = %s, want %s", res.Status, core.OrderStatusPending)
	}

	if res.ProviderRef != "APIKUY_PENDING_9999" {
		t.Errorf("ProviderRef = %s, want APIKUY_PENDING_9999", res.ProviderRef)
	}
}
