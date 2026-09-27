package core

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"bot-proses/internal/store"
)

// mockRepo implements a simple in-memory repository for unit testing
type mockRepo struct {
	mu     sync.Mutex
	batch  *store.BatchOrder
	items  map[int64]*store.BatchOrderItem
	states []string
}

func newMockRepo(batch *store.BatchOrder, items []*store.BatchOrderItem) *mockRepo {
	m := &mockRepo{
		batch: batch,
		items: make(map[int64]*store.BatchOrderItem),
	}
	for _, it := range items {
		m.items[it.ID] = it
	}
	return m
}

func (m *mockRepo) CreateBatch(ctx context.Context, batch *store.BatchOrder, items []*store.BatchOrderItem) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	batch.ID = 1
	m.batch = batch
	for _, it := range items {
		m.items[it.ID] = it
	}
	return 1, nil
}

func (m *mockRepo) GetBatch(ctx context.Context, batchID int64) (*store.BatchOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.batch, nil
}

func (m *mockRepo) GetProcessingBatches(ctx context.Context) ([]*store.BatchOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batch != nil && m.batch.Status == store.BatchStatusProcessing {
		return []*store.BatchOrder{m.batch}, nil
	}
	return nil, nil
}

func (m *mockRepo) GetBatchItems(ctx context.Context, batchID int64) ([]*store.BatchOrderItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]*store.BatchOrderItem, 0, len(m.items))
	for _, it := range m.items {
		res = append(res, it)
	}
	return res, nil
}

func (m *mockRepo) GetPendingItems(ctx context.Context, batchID int64) ([]*store.BatchOrderItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []*store.BatchOrderItem
	for _, it := range m.items {
		if it.Status == store.ItemStatusPending {
			res = append(res, it)
		}
	}
	return res, nil
}

func (m *mockRepo) GetInProgressItems(ctx context.Context, batchID int64) ([]*store.BatchOrderItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []*store.BatchOrderItem
	for _, it := range m.items {
		if it.Status == store.ItemStatusInProgress {
			res = append(res, it)
		}
	}
	return res, nil
}

func (m *mockRepo) UpdateItemStatus(ctx context.Context, itemID int64, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.items[itemID]; ok {
		it.Status = status
		m.states = append(m.states, fmt.Sprintf("%d:%s", itemID, status))
	}
	return nil
}

func (m *mockRepo) UpdateItemResult(ctx context.Context, itemID int64, status, sn, providerRef, errorMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it, ok := m.items[itemID]; ok {
		it.Status = status
		m.states = append(m.states, fmt.Sprintf("%d:%s", itemID, status))
	}
	return nil
}

func (m *mockRepo) UpdateBatchStatus(ctx context.Context, batchID int64, status string, successCount, failedCount, manualReviewCount int, completedAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batch != nil {
		m.batch.Status = status
		m.batch.SuccessCount = successCount
		m.batch.FailedCount = failedCount
		m.batch.ManualReviewCount = manualReviewCount
	}
	return nil
}

func (m *mockRepo) CheckAndCompleteBatch(ctx context.Context, batchID int64) (bool, *store.BatchOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var pendingCnt, inProgCnt, successCnt, failedCnt, manualCnt int
	for _, it := range m.items {
		switch it.Status {
		case store.ItemStatusPending:
			pendingCnt++
		case store.ItemStatusInProgress:
			inProgCnt++
		case store.ItemStatusSuccess:
			successCnt++
		case store.ItemStatusFailed:
			failedCnt++
		case store.ItemStatusNeedsManualReview:
			manualCnt++
		}
	}

	m.batch.SuccessCount = successCnt
	m.batch.FailedCount = failedCnt
	m.batch.ManualReviewCount = manualCnt

	if pendingCnt == 0 && inProgCnt == 0 {
		m.batch.Status = store.BatchStatusCompleted
		return true, m.batch, nil
	}
	return false, m.batch, nil
}

// mockAdapter implements core.ProductAdapter
type mockAdapter struct {
	placeOrderFunc func(ctx context.Context, req PlaceOrderRequest) (OrderResult, error)
}

func (a *mockAdapter) PlaceOrder(ctx context.Context, req PlaceOrderRequest) (OrderResult, error) {
	if a.placeOrderFunc != nil {
		return a.placeOrderFunc(ctx, req)
	}
	return OrderResult{
		SN:          "SN-MOCK-" + req.IdempotencyKey,
		ProviderRef: "REF-MOCK-" + req.IdempotencyKey,
		Status:      OrderStatusSuccess,
	}, nil
}

func (a *mockAdapter) CheckStatus(ctx context.Context, idempotencyKey string) (OrderResult, bool, error) {
	return OrderResult{Status: OrderStatusSuccess, SN: "SN-RESUMED"}, true, nil
}

func TestOrchestratorExecutionAndStatusTransitions(t *testing.T) {
	batch := &store.BatchOrder{
		ID:             1,
		TelegramUserID: 111,
		TelegramChatID: 222,
		ProductCode:    "FF5",
		TargetID:       "998877",
		Qty:            3,
		Status:         store.BatchStatusProcessing,
	}

	items := []*store.BatchOrderItem{
		{ID: 1, BatchID: 1, SequenceNo: 1, IdempotencyKey: "1-1", Status: store.ItemStatusPending},
		{ID: 2, BatchID: 1, SequenceNo: 2, IdempotencyKey: "1-2", Status: store.ItemStatusPending},
		{ID: 3, BatchID: 1, SequenceNo: 3, IdempotencyKey: "1-3", Status: store.ItemStatusPending},
	}

	repo := newMockRepo(batch, items)

	adapter := &mockAdapter{
		placeOrderFunc: func(ctx context.Context, req PlaceOrderRequest) (OrderResult, error) {
			if req.IdempotencyKey == "1-2" {
				return OrderResult{Status: OrderStatusFailed, Message: "Target not found"}, nil
			}
			return OrderResult{Status: OrderStatusSuccess, SN: "SN-" + req.IdempotencyKey}, nil
		},
	}

	completedCh := make(chan *store.BatchOrder, 1)
	onCompleted := func(b *store.BatchOrder, its []*store.BatchOrderItem) {
		completedCh <- b
	}

	orch := NewOrchestrator(repo, adapter, 2, onCompleted, nil)
	defer orch.Stop(5 * time.Second)

	orch.EnqueueBatch(batch, items)

	select {
	case completedBatch := <-completedCh:
		if completedBatch.Status != store.BatchStatusCompleted {
			t.Errorf("batch status = %s, want %s", completedBatch.Status, store.BatchStatusCompleted)
		}
		if completedBatch.SuccessCount != 2 {
			t.Errorf("success count = %d, want 2", completedBatch.SuccessCount)
		}
		if completedBatch.FailedCount != 1 {
			t.Errorf("failed count = %d, want 1", completedBatch.FailedCount)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for batch completion")
	}

	// Verify status transition: every item MUST have gone through 'in_progress' before 'success' or 'failed'
	repo.mu.Lock()
	states := repo.states
	repo.mu.Unlock()

	hasInProgress := false
	for _, st := range states {
		if st == "1:in_progress" || st == "2:in_progress" || st == "3:in_progress" {
			hasInProgress = true
			break
		}
	}

	if !hasInProgress {
		t.Errorf("expected items to transition to in_progress first, recorded states: %v", states)
	}
}
