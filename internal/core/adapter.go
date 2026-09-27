package core

import "context"

const (
	OrderStatusSuccess = "success"
	OrderStatusFailed  = "failed"
	OrderStatusPending = "pending"
)

// PlaceOrderRequest contains the parameters required to place an individual order item.
type PlaceOrderRequest struct {
	ProductCode    string `json:"product_code"`
	TargetID       string `json:"target_id"`
	IdempotencyKey string `json:"idempotency_key"` // Format: "<batch_id>-<sequence_no>"
}

// OrderResult represents the result returned by a product provider adapter.
type OrderResult struct {
	SN          string `json:"sn"`
	ProviderRef string `json:"provider_ref"`
	Status      string `json:"status"` // OrderStatusSuccess or OrderStatusFailed
	Message     string `json:"message"`
}

// ProductAdapter is the generic interface connecting core logic to external provider APIs or scrapers.
type ProductAdapter interface {
	// PlaceOrder executes the order on the provider system.
	PlaceOrder(ctx context.Context, req PlaceOrderRequest) (OrderResult, error)

	// CheckStatus verifies the real status of an order on the provider side using its idempotency key.
	// The returned bool indicates whether CheckStatus is supported by the provider adapter.
	// If false, the caller will mark the item as 'needs_manual_review' during resume recovery.
	CheckStatus(ctx context.Context, idempotencyKey string) (result OrderResult, supported bool, err error)
}
