package core

import (
	"context"
	"testing"
	"time"

	"bot-proses/internal/store"
)

type mockResumeAdapter struct {
	checkStatusFunc func(ctx context.Context, idempotencyKey string) (OrderResult, bool, error)
}

func (m *mockResumeAdapter) PlaceOrder(ctx context.Context, req PlaceOrderRequest) (OrderResult, error) {
	return OrderResult{Status: OrderStatusSuccess, SN: "SN-RESUMED-" + req.IdempotencyKey}, nil
}

func (m *mockResumeAdapter) CheckStatus(ctx context.Context, idempotencyKey string) (OrderResult, bool, error) {
	if m.checkStatusFunc != nil {
		return m.checkStatusFunc(ctx, idempotencyKey)
	}
	return OrderResult{}, false, nil
}

func TestResumeManagerUnsupportedCheckStatus(t *testing.T) {
	batch := &store.BatchOrder{
		ID:             5,
		ProductCode:    "FF5",
		TargetID:       "12345",
		Qty:            1,
		Status:         store.BatchStatusProcessing,
	}

	items := []*store.BatchOrderItem{
		{
			ID:             501,
			BatchID:        5,
			SequenceNo:     1,
			IdempotencyKey: "5-1",
			Status:         store.ItemStatusInProgress,
		},
	}

	repo := newMockRepo(batch, items)

	// Adapter returns supported = false (provider has no status check API)
	adapter := &mockResumeAdapter{
		checkStatusFunc: func(ctx context.Context, idempotencyKey string) (OrderResult, bool, error) {
			return OrderResult{}, false, nil
		},
	}

	orch := NewOrchestrator(repo, adapter, 2, nil, nil)
	defer orch.Stop(1 * time.Second)

	resumeMgr := NewResumeManager(repo, adapter, orch, nil)
	err := resumeMgr.ResumeUnfinishedBatches(context.Background())
	if err != nil {
		t.Fatalf("ResumeUnfinishedBatches failed: %v", err)
	}

	// Item must be marked as needs_manual_review
	repo.mu.Lock()
	item := repo.items[501]
	repo.mu.Unlock()

	if item.Status != store.ItemStatusNeedsManualReview {
		t.Errorf("expected item status %s, got %s", store.ItemStatusNeedsManualReview, item.Status)
	}
}

func TestResumeManagerSupportedCheckStatusSuccess(t *testing.T) {
	batch := &store.BatchOrder{
		ID:             6,
		ProductCode:    "FF5",
		TargetID:       "12345",
		Qty:            1,
		Status:         store.BatchStatusProcessing,
	}

	items := []*store.BatchOrderItem{
		{
			ID:             601,
			BatchID:        6,
			SequenceNo:     1,
			IdempotencyKey: "6-1",
			Status:         store.ItemStatusInProgress,
		},
	}

	repo := newMockRepo(batch, items)

	// Adapter returns supported = true, Status = success
	adapter := &mockResumeAdapter{
		checkStatusFunc: func(ctx context.Context, idempotencyKey string) (OrderResult, bool, error) {
			return OrderResult{Status: OrderStatusSuccess, SN: "SN-CONFIRMED-601"}, true, nil
		},
	}

	orch := NewOrchestrator(repo, adapter, 2, nil, nil)
	defer orch.Stop(1 * time.Second)

	resumeMgr := NewResumeManager(repo, adapter, orch, nil)
	err := resumeMgr.ResumeUnfinishedBatches(context.Background())
	if err != nil {
		t.Fatalf("ResumeUnfinishedBatches failed: %v", err)
	}

	repo.mu.Lock()
	item := repo.items[601]
	repo.mu.Unlock()

	if item.Status != store.ItemStatusSuccess {
		t.Errorf("expected item status %s, got %s", store.ItemStatusSuccess, item.Status)
	}
}
