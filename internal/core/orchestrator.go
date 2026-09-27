package core

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"bot-proses/internal/store"
)

// BatchCompletionCallback is invoked when a batch finishes processing all its items.
type BatchCompletionCallback func(batch *store.BatchOrder, items []*store.BatchOrderItem)

type Orchestrator struct {
	repo         store.Repository
	adapter      ProductAdapter
	concurrency  int
	onCompleted  BatchCompletionCallback
	logger       *slog.Logger

	// Dispatch control & in-flight tracking
	mu           sync.Mutex
	activeJobs   chan *batchItemJob
	inFlightWg   sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	isStopped    bool
}

type batchItemJob struct {
	Batch   *store.BatchOrder
	Item    *store.BatchOrderItem
	DoneCh  chan struct{}
}

func NewOrchestrator(
	repo store.Repository,
	adapter ProductAdapter,
	concurrency int,
	onCompleted BatchCompletionCallback,
	logger *slog.Logger,
) *Orchestrator {
	if concurrency <= 0 {
		concurrency = 5
	}
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	orch := &Orchestrator{
		repo:        repo,
		adapter:     adapter,
		concurrency: concurrency,
		onCompleted: onCompleted,
		logger:      logger,
		activeJobs:  make(chan *batchItemJob, 1000),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Start worker pool
	for i := 1; i <= concurrency; i++ {
		go orch.worker(i)
	}

	return orch
}

// EnqueueBatch processes all pending items of a batch asynchronously.
func (o *Orchestrator) EnqueueBatch(batch *store.BatchOrder, pendingItems []*store.BatchOrderItem) {
	o.mu.Lock()
	if o.isStopped {
		o.mu.Unlock()
		o.logger.Warn("cannot enqueue batch: orchestrator stopped", "batch_id", batch.ID)
		return
	}
	o.mu.Unlock()

	go func() {
		o.logger.Info("enqueuing batch for execution", "batch_id", batch.ID, "items_count", len(pendingItems))

		var batchWg sync.WaitGroup
		for _, item := range pendingItems {
			select {
			case <-o.ctx.Done():
				o.logger.Warn("stopping batch enqueue due to shutdown", "batch_id", batch.ID, "item_seq", item.SequenceNo)
				return
			default:
				batchWg.Add(1)
				job := &batchItemJob{
					Batch:  batch,
					Item:   item,
					DoneCh: make(chan struct{}),
				}

				// Track in-flight globally
				o.inFlightWg.Add(1)
				o.activeJobs <- job

				// Asynchronously wait for this item and decrement batchWg
				go func(j *batchItemJob) {
					<-j.DoneCh
					batchWg.Done()
				}(job)
			}
		}

		// Wait until all dispatched items for this batch complete
		batchWg.Wait()

		// Check if batch is fully completed
		o.finalizeBatchIfDone(context.Background(), batch.ID)
	}()
}

func (o *Orchestrator) worker(workerID int) {
	for job := range o.activeJobs {
		o.processItemSafely(workerID, job)
	}
}

func (o *Orchestrator) processItemSafely(workerID int, job *batchItemJob) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			o.logger.Error("panic recovered in worker",
				"worker_id", workerID,
				"batch_id", job.Batch.ID,
				"item_id", job.Item.ID,
				"seq", job.Item.SequenceNo,
				"panic", r,
				"stack", stack,
			)

			// Update item to failed upon panic so it is not stuck indefinitely
			errCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := o.repo.UpdateItemResult(errCtx, job.Item.ID, store.ItemStatusFailed, "", "", fmt.Sprintf("panic in worker: %v", r)); err != nil {
				o.logger.Error("failed to persist panic item result", "item_id", job.Item.ID, "err", err)
			}
			cancel()
		}
		close(job.DoneCh)
		o.inFlightWg.Done()
	}()

	item := job.Item
	batch := job.Batch

	o.logger.Debug("worker started item",
		"worker_id", workerID,
		"batch_id", batch.ID,
		"item_id", item.ID,
		"seq", item.SequenceNo,
	)

	// Step 1: Update status to 'in_progress' and commit to DB before calling adapter
	dbCtx, cancelDB := context.WithTimeout(context.Background(), 5*time.Second)
	if err := o.repo.UpdateItemStatus(dbCtx, item.ID, store.ItemStatusInProgress); err != nil {
		cancelDB()
		o.logger.Error("failed to set item status to in_progress", "item_id", item.ID, "err", err)
		return
	}
	cancelDB()

	// Step 2: Call adapter.PlaceOrder with deterministic idempotency key
	orderReq := PlaceOrderRequest{
		ProductCode:    batch.ProductCode,
		TargetID:       batch.TargetID,
		IdempotencyKey: item.IdempotencyKey,
	}

	// We use Background with timeout to ensure in-flight orders can complete even if main ctx is canceling
	reqCtx, cancelReq := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReq()

	res, err := o.adapter.PlaceOrder(reqCtx, orderReq)

	// Step 3: Commit result to DB (success / failed)
	dbSaveCtx, cancelDBSave := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDBSave()

	if err != nil || res.Status == OrderStatusFailed {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else {
			errMsg = res.Message
		}
		o.logger.Warn("order item failed",
			"batch_id", batch.ID,
			"seq", item.SequenceNo,
			"idempotency_key", item.IdempotencyKey,
			"error", errMsg,
		)
		if err := o.repo.UpdateItemResult(dbSaveCtx, item.ID, store.ItemStatusFailed, res.SN, res.ProviderRef, errMsg); err != nil {
			o.logger.Error("failed to persist failed item result", "batch_id", batch.ID, "item_id", item.ID, "err", err)
		}
	} else {
		o.logger.Info("order item succeeded",
			"batch_id", batch.ID,
			"seq", item.SequenceNo,
			"idempotency_key", item.IdempotencyKey,
			"sn", res.SN,
		)
		if err := o.repo.UpdateItemResult(dbSaveCtx, item.ID, store.ItemStatusSuccess, res.SN, res.ProviderRef, ""); err != nil {
			o.logger.Error("failed to persist success item result", "batch_id", batch.ID, "item_id", item.ID, "err", err)
		}
	}
}

func (o *Orchestrator) finalizeBatchIfDone(ctx context.Context, batchID int64) {
	isCompleted, updatedBatch, err := o.repo.CheckAndCompleteBatch(ctx, batchID)
	if err != nil {
		o.logger.Error("failed to check batch completion", "batch_id", batchID, "err", err)
		return
	}

	if isCompleted && updatedBatch != nil {
		o.logger.Info("batch fully completed",
			"batch_id", batchID,
			"success", updatedBatch.SuccessCount,
			"failed", updatedBatch.FailedCount,
			"manual_review", updatedBatch.ManualReviewCount,
		)

		if o.onCompleted != nil {
			allItems, err := o.repo.GetBatchItems(ctx, batchID)
			if err == nil {
				o.onCompleted(updatedBatch, allItems)
			} else {
				o.logger.Error("failed to fetch items for completion callback", "batch_id", batchID, "err", err)
			}
		}
	}
}

// Stop gracefully stops the orchestrator by draining in-flight workers up to the specified timeout.
func (o *Orchestrator) Stop(timeout time.Duration) {
	o.mu.Lock()
	if o.isStopped {
		o.mu.Unlock()
		return
	}
	o.isStopped = true
	o.cancel()
	o.mu.Unlock()

	o.logger.Info("orchestrator stopping: waiting for in-flight worker tasks to finish...", "timeout", timeout)

	// Wait for in-flight workers with timeout
	done := make(chan struct{})
	go func() {
		o.inFlightWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		o.logger.Info("all in-flight worker tasks finished gracefully")
	case <-time.After(timeout):
		o.logger.Warn("orchestrator stop timed out; some tasks may remain in-progress")
	}

	close(o.activeJobs)
}
