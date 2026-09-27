package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"bot-proses/internal/store"
)

// StatusPoller periodically queries the provider for items that are stuck in 'in_progress' status
// to ensure batches finalize even if async webhook callbacks are delayed or lost.
type StatusPoller struct {
	repo         store.Repository
	adapter      ProductAdapter
	orchestrator *Orchestrator
	interval     time.Duration
	logger       *slog.Logger
	stopCh       chan struct{}
	wg           sync.WaitGroup
	mu           sync.Mutex
}

func NewStatusPoller(
	repo store.Repository,
	adapter ProductAdapter,
	orchestrator *Orchestrator,
	interval time.Duration,
	logger *slog.Logger,
) *StatusPoller {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &StatusPoller{
		repo:         repo,
		adapter:      adapter,
		orchestrator: orchestrator,
		interval:     interval,
		logger:       logger,
		stopCh:       make(chan struct{}),
	}
}

// Start launches the background periodic polling loop.
func (p *StatusPoller) Start() {
	p.logger.Info("starting periodic status poller", "interval", p.interval.String())
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		for {
			select {
			case <-p.stopCh:
				p.logger.Info("status poller stopped")
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				p.PollBatches(ctx)
				cancel()
			}
		}
	}()
}

// Stop gracefully stops the status poller.
func (p *StatusPoller) Stop() {
	close(p.stopCh)
	p.wg.Wait()
}

// PollBatches queries all processing batches and syncs their in-progress items.
func (p *StatusPoller) PollBatches(ctx context.Context) {
	batches, err := p.repo.GetProcessingBatches(ctx)
	if err != nil {
		p.logger.Error("status poller: failed to get processing batches", "err", err)
		return
	}

	if len(batches) == 0 {
		return
	}

	p.logger.Debug("status poller: checking processing batches", "count", len(batches))

	for _, batch := range batches {
		select {
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		if err := p.syncBatchItems(ctx, batch); err != nil {
			p.logger.Error("status poller: error syncing batch", "batch_id", batch.ID, "err", err)
		}
	}
}

// SyncBatch synchronizes a single batch on demand (e.g. called from /status telegram command).
func (p *StatusPoller) SyncBatch(ctx context.Context, batchID int64) (*store.BatchOrder, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	batch, err := p.repo.GetBatch(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch batch #%d: %w", batchID, err)
	}
	if batch == nil {
		return nil, fmt.Errorf("batch #%d not found", batchID)
	}

	// Only sync if batch is currently processing
	if batch.Status == store.BatchStatusProcessing {
		if err := p.syncBatchItems(ctx, batch); err != nil {
			return nil, err
		}
		// Re-fetch updated batch after sync
		batch, err = p.repo.GetBatch(ctx, batchID)
		if err != nil {
			return nil, fmt.Errorf("failed to re-fetch batch #%d: %w", batchID, err)
		}
	}

	return batch, nil
}

func (p *StatusPoller) syncBatchItems(ctx context.Context, batch *store.BatchOrder) error {
	inProgItems, err := p.repo.GetInProgressItems(ctx, batch.ID)
	if err != nil {
		return fmt.Errorf("failed to get in_progress items for batch %d: %w", batch.ID, err)
	}

	if len(inProgItems) == 0 {
		// No in_progress items; check if batch can be finalized
		p.orchestrator.CheckAndFinalizeBatch(ctx, batch.ID)
		return nil
	}

	p.logger.Info("status poller: syncing in_progress items",
		"batch_id", batch.ID,
		"in_progress_count", len(inProgItems),
	)

	for _, item := range inProgItems {
		select {
		case <-p.stopCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		itemCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		res, supported, err := p.adapter.CheckStatus(itemCtx, item.IdempotencyKey)
		cancel()

		if !supported {
			p.logger.Warn("provider does not support status check", "item_id", item.ID)
			continue
		}

		if err != nil {
			p.logger.Warn("error checking item status from provider",
				"batch_id", batch.ID,
				"item_id", item.ID,
				"idempotency_key", item.IdempotencyKey,
				"err", err,
			)
			continue
		}

		switch res.Status {
		case OrderStatusSuccess:
			p.logger.Info("status poller: item confirmed success",
				"batch_id", batch.ID,
				"seq", item.SequenceNo,
				"sn", res.SN,
			)
			_ = p.repo.UpdateItemResult(ctx, item.ID, store.ItemStatusSuccess, res.SN, res.ProviderRef, "")

		case OrderStatusFailed:
			p.logger.Info("status poller: item confirmed failed",
				"batch_id", batch.ID,
				"seq", item.SequenceNo,
				"err", res.Message,
			)
			_ = p.repo.UpdateItemResult(ctx, item.ID, store.ItemStatusFailed, res.SN, res.ProviderRef, res.Message)

		case OrderStatusPending:
			p.logger.Debug("status poller: item still pending",
				"batch_id", batch.ID,
				"seq", item.SequenceNo,
			)
			if res.ProviderRef != "" && !item.ProviderRef.Valid {
				_ = p.repo.UpdateItemProviderRef(ctx, item.ID, res.ProviderRef)
			}
		default:
			// Status unknown or not found, keep in_progress for next check
			p.logger.Debug("status poller: item status unknown",
				"batch_id", batch.ID,
				"seq", item.SequenceNo,
				"status", res.Status,
			)
		}
	}

	// Finalize batch if all items reached final state
	p.orchestrator.CheckAndFinalizeBatch(ctx, batch.ID)
	return nil
}
