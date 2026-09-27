package store

import (
	"context"
	"time"
)

type Repository interface {
	// CreateBatch creates a new batch order and all its initial pending items within a single transaction.
	CreateBatch(ctx context.Context, batch *BatchOrder, items []*BatchOrderItem) (int64, error)

	// GetBatch retrieves a batch by ID.
	GetBatch(ctx context.Context, batchID int64) (*BatchOrder, error)

	// GetProcessingBatches retrieves all batches currently in 'processing' status.
	GetProcessingBatches(ctx context.Context) ([]*BatchOrder, error)

	// GetBatchItems retrieves all items belonging to a batch, ordered by sequence_no.
	GetBatchItems(ctx context.Context, batchID int64) ([]*BatchOrderItem, error)

	// GetPendingItems retrieves all items with 'pending' status for a given batch.
	GetPendingItems(ctx context.Context, batchID int64) ([]*BatchOrderItem, error)

	// GetInProgressItems retrieves all items with 'in_progress' status for a given batch.
	GetInProgressItems(ctx context.Context, batchID int64) ([]*BatchOrderItem, error)

	// UpdateItemStatus updates the status of an item (e.g. to in_progress or needs_manual_review).
	UpdateItemStatus(ctx context.Context, itemID int64, status string) error

	// UpdateItemResult updates the final result of an item (status, sn, provider_ref, error_message).
	UpdateItemResult(ctx context.Context, itemID int64, status, sn, providerRef, errorMsg string) error

	// UpdateBatchStatus updates the batch's counts and overall status.
	UpdateBatchStatus(ctx context.Context, batchID int64, status string, successCount, failedCount, manualReviewCount int, completedAt *time.Time) error

	// CheckAndCompleteBatch checks if all items for a batch have reached final state (success, failed, needs_manual_review)
	// and if so, updates the batch to completed.
	CheckAndCompleteBatch(ctx context.Context, batchID int64) (isCompleted bool, batch *BatchOrder, err error)
}
