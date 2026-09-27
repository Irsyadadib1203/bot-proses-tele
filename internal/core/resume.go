package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"bot-proses/internal/store"
)

type ResumeManager struct {
	repo         store.Repository
	adapter      ProductAdapter
	orchestrator *Orchestrator
	logger       *slog.Logger
}

func NewResumeManager(
	repo store.Repository,
	adapter ProductAdapter,
	orchestrator *Orchestrator,
	logger *slog.Logger,
) *ResumeManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResumeManager{
		repo:         repo,
		adapter:      adapter,
		orchestrator: orchestrator,
		logger:       logger,
	}
}

// ResumeUnfinishedBatches checks all batches with 'processing' status and processes pending/in_progress items safely.
func (r *ResumeManager) ResumeUnfinishedBatches(ctx context.Context) error {
	r.logger.Info("checking for unfinished batches to resume...")

	batches, err := r.repo.GetProcessingBatches(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch processing batches: %w", err)
	}

	if len(batches) == 0 {
		r.logger.Info("no unfinished batches found")
		return nil
	}

	r.logger.Info("found unfinished batches", "count", len(batches))

	for _, batch := range batches {
		r.logger.Info("resuming batch", "batch_id", batch.ID, "product", batch.ProductCode, "target_id", batch.TargetID)

		// 1. Handle in_progress items first
		inProgItems, err := r.repo.GetInProgressItems(ctx, batch.ID)
		if err != nil {
			r.logger.Error("failed to fetch in_progress items for batch", "batch_id", batch.ID, "err", err)
			continue
		}

		for _, item := range inProgItems {
			r.handleInProgressItem(ctx, batch, item)
		}

		// 2. Fetch pending items (including any item reset to pending from check status)
		pendingItems, err := r.repo.GetPendingItems(ctx, batch.ID)
		if err != nil {
			r.logger.Error("failed to fetch pending items for batch", "batch_id", batch.ID, "err", err)
			continue
		}

		if len(pendingItems) > 0 {
			r.logger.Info("dispatching pending items for resumed batch", "batch_id", batch.ID, "pending_count", len(pendingItems))
			r.orchestrator.EnqueueBatch(batch, pendingItems)
		} else {
			// No pending items left, check if all items are final
			r.orchestrator.finalizeBatchIfDone(ctx, batch.ID)
		}
	}

	return nil
}

func (r *ResumeManager) handleInProgressItem(ctx context.Context, batch *store.BatchOrder, item *store.BatchOrderItem) {
	r.logger.Info("evaluating in_progress item from interrupted run",
		"batch_id", batch.ID,
		"seq", item.SequenceNo,
		"idempotency_key", item.IdempotencyKey,
	)

	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	res, supported, err := r.adapter.CheckStatus(checkCtx, item.IdempotencyKey)
	if !supported {
		// Provider does not support checking status; mark as needs_manual_review to prevent double-ordering
		r.logger.Warn("provider does not support CheckStatus; marking item as needs_manual_review",
			"batch_id", batch.ID,
			"seq", item.SequenceNo,
			"idempotency_key", item.IdempotencyKey,
		)
		_ = r.repo.UpdateItemResult(
			ctx,
			item.ID,
			store.ItemStatusNeedsManualReview,
			"",
			"",
			"Interrupted during execution. Provider does not support status check; manual review required to prevent double-order.",
		)
		return
	}

	if err != nil {
		r.logger.Error("error checking status from provider; marking as needs_manual_review",
			"batch_id", batch.ID,
			"seq", item.SequenceNo,
			"idempotency_key", item.IdempotencyKey,
			"err", err,
		)
		_ = r.repo.UpdateItemResult(
			ctx,
			item.ID,
			store.ItemStatusNeedsManualReview,
			"",
			"",
			fmt.Sprintf("Failed to verify status on provider during resume: %v", err),
		)
		return
	}

	switch res.Status {
	case OrderStatusSuccess:
		r.logger.Info("in_progress item confirmed successful by provider",
			"batch_id", batch.ID,
			"seq", item.SequenceNo,
			"sn", res.SN,
		)
		_ = r.repo.UpdateItemResult(ctx, item.ID, store.ItemStatusSuccess, res.SN, res.ProviderRef, "")
	case OrderStatusFailed:
		r.logger.Info("in_progress item confirmed failed by provider",
			"batch_id", batch.ID,
			"seq", item.SequenceNo,
			"err", res.Message,
		)
		_ = r.repo.UpdateItemResult(ctx, item.ID, store.ItemStatusFailed, res.SN, res.ProviderRef, res.Message)
	default:
		// Not yet processed / unknown on provider -> safe to reset to pending for re-execution
		r.logger.Info("in_progress item not found on provider; resetting to pending for safe processing",
			"batch_id", batch.ID,
			"seq", item.SequenceNo,
		)
		_ = r.repo.UpdateItemStatus(ctx, item.ID, store.ItemStatusPending)
	}
}
