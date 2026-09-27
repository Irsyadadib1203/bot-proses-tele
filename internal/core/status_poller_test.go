package core

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"bot-proses/internal/store"
)

func TestStatusPollerSyncBatchSuccess(t *testing.T) {
	batch := &store.BatchOrder{
		ID:          10,
		ProductCode: "FF5",
		TargetID:    "12345",
		Qty:         2,
		Status:      store.BatchStatusProcessing,
	}

	items := []*store.BatchOrderItem{
		{
			ID:             1001,
			BatchID:        10,
			SequenceNo:     1,
			IdempotencyKey: "10-1",
			Status:         store.ItemStatusInProgress,
		},
		{
			ID:             1002,
			BatchID:        10,
			SequenceNo:     2,
			IdempotencyKey: "10-2",
			Status:         store.ItemStatusInProgress,
		},
	}

	repo := newMockRepo(batch, items)

	adapter := &mockResumeAdapter{
		checkStatusFunc: func(ctx context.Context, idempotencyKey string) (OrderResult, bool, error) {
			return OrderResult{
				Status:      OrderStatusSuccess,
				SN:          "SN-" + idempotencyKey,
				ProviderRef: "REF-" + idempotencyKey,
			}, true, nil
		},
	}

	var completedCalled int32
	onCompleted := func(b *store.BatchOrder, its []*store.BatchOrderItem) {
		atomic.AddInt32(&completedCalled, 1)
	}

	orch := NewOrchestrator(repo, adapter, 2, onCompleted, nil)
	defer orch.Stop(1 * time.Second)

	poller := NewStatusPoller(repo, adapter, orch, 1*time.Minute, nil)

	syncedBatch, err := poller.SyncBatch(context.Background(), 10)
	if err != nil {
		t.Fatalf("SyncBatch failed: %v", err)
	}

	if syncedBatch.Status != store.BatchStatusCompleted {
		t.Errorf("expected batch status %s, got %s", store.BatchStatusCompleted, syncedBatch.Status)
	}

	repo.mu.Lock()
	it1 := repo.items[1001]
	it2 := repo.items[1002]
	repo.mu.Unlock()

	if it1.Status != store.ItemStatusSuccess || it1.SN.String != "SN-10-1" {
		t.Errorf("unexpected item 1: status=%s, sn=%s", it1.Status, it1.SN.String)
	}
	if it2.Status != store.ItemStatusSuccess || it2.SN.String != "SN-10-2" {
		t.Errorf("unexpected item 2: status=%s, sn=%s", it2.Status, it2.SN.String)
	}

	// Give a moment for completion goroutine
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&completedCalled) != 1 {
		t.Errorf("expected onCompleted called once, got %d", atomic.LoadInt32(&completedCalled))
	}
}

func TestStatusPollerSyncBatchMixed(t *testing.T) {
	batch := &store.BatchOrder{
		ID:          11,
		ProductCode: "ML5",
		TargetID:    "67890",
		Qty:         3,
		Status:      store.BatchStatusProcessing,
	}

	items := []*store.BatchOrderItem{
		{
			ID:             1101,
			BatchID:        11,
			SequenceNo:     1,
			IdempotencyKey: "11-1",
			Status:         store.ItemStatusInProgress,
		},
		{
			ID:             1102,
			BatchID:        11,
			SequenceNo:     2,
			IdempotencyKey: "11-2",
			Status:         store.ItemStatusInProgress,
		},
		{
			ID:             1103,
			BatchID:        11,
			SequenceNo:     3,
			IdempotencyKey: "11-3",
			Status:         store.ItemStatusInProgress,
			ProviderRef:    sql.NullString{Valid: false},
		},
	}

	repo := newMockRepo(batch, items)

	adapter := &mockResumeAdapter{
		checkStatusFunc: func(ctx context.Context, idempotencyKey string) (OrderResult, bool, error) {
			switch idempotencyKey {
			case "11-1":
				return OrderResult{Status: OrderStatusSuccess, SN: "SN-11-1"}, true, nil
			case "11-2":
				return OrderResult{Status: OrderStatusFailed, Message: "insufficient balance"}, true, nil
			case "11-3":
				return OrderResult{Status: OrderStatusPending, ProviderRef: "REF-11-3"}, true, nil
			default:
				return OrderResult{}, false, nil
			}
		},
	}

	orch := NewOrchestrator(repo, adapter, 2, nil, nil)
	defer orch.Stop(1 * time.Second)

	poller := NewStatusPoller(repo, adapter, orch, 1*time.Minute, nil)

	syncedBatch, err := poller.SyncBatch(context.Background(), 11)
	if err != nil {
		t.Fatalf("SyncBatch failed: %v", err)
	}

	// Should still be processing because item 1103 is pending
	if syncedBatch.Status != store.BatchStatusProcessing {
		t.Errorf("expected batch status %s, got %s", store.BatchStatusProcessing, syncedBatch.Status)
	}

	repo.mu.Lock()
	it1 := repo.items[1101]
	it2 := repo.items[1102]
	it3 := repo.items[1103]
	repo.mu.Unlock()

	if it1.Status != store.ItemStatusSuccess {
		t.Errorf("expected it1 success, got %s", it1.Status)
	}
	if it2.Status != store.ItemStatusFailed {
		t.Errorf("expected it2 failed, got %s", it2.Status)
	}
	if it3.Status != store.ItemStatusInProgress {
		t.Errorf("expected it3 in_progress, got %s", it3.Status)
	}
	if !it3.ProviderRef.Valid || it3.ProviderRef.String != "REF-11-3" {
		t.Errorf("expected it3 provider_ref REF-11-3, got %s", it3.ProviderRef.String)
	}
}

func TestStatusPollerBackgroundLoop(t *testing.T) {
	batch := &store.BatchOrder{
		ID:          12,
		ProductCode: "FF5",
		TargetID:    "12345",
		Qty:         1,
		Status:      store.BatchStatusProcessing,
	}

	items := []*store.BatchOrderItem{
		{
			ID:             1201,
			BatchID:        12,
			SequenceNo:     1,
			IdempotencyKey: "12-1",
			Status:         store.ItemStatusInProgress,
		},
	}

	repo := newMockRepo(batch, items)

	adapter := &mockResumeAdapter{
		checkStatusFunc: func(ctx context.Context, idempotencyKey string) (OrderResult, bool, error) {
			return OrderResult{Status: OrderStatusSuccess, SN: "SN-AUTO-12-1"}, true, nil
		},
	}

	orch := NewOrchestrator(repo, adapter, 2, nil, nil)
	defer orch.Stop(1 * time.Second)

	// Short interval for testing
	poller := NewStatusPoller(repo, adapter, orch, 50*time.Millisecond, nil)
	poller.Start()

	// Wait for poller to run at least one tick
	time.Sleep(150 * time.Millisecond)
	poller.Stop()

	repo.mu.Lock()
	it := repo.items[1201]
	b := repo.batch
	repo.mu.Unlock()

	if it.Status != store.ItemStatusSuccess {
		t.Errorf("expected item status success from background poller, got %s", it.Status)
	}
	if b.Status != store.BatchStatusCompleted {
		t.Errorf("expected batch status completed, got %s", b.Status)
	}
}
