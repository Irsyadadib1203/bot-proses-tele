package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type MySQLRepository struct {
	db *sql.DB
}

func NewMySQLRepository(db *sql.DB) *MySQLRepository {
	return &MySQLRepository{db: db}
}

func (r *MySQLRepository) CreateBatch(ctx context.Context, batch *BatchOrder, items []*BatchOrderItem) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO batch_orders (telegram_user_id, telegram_chat_id, product_code, target_id, qty, status, success_count, failed_count, manual_review_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, batch.TelegramUserID, batch.TelegramChatID, batch.ProductCode, batch.TargetID, batch.Qty, BatchStatusProcessing, 0, 0, 0, now)
	if err != nil {
		return 0, fmt.Errorf("failed to insert batch_order: %w", err)
	}

	batchID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}
	batch.ID = batchID
	batch.CreatedAt = now
	batch.Status = BatchStatusProcessing

	if len(items) > 0 {
		// Batch insert items in chunks to avoid max placeholder limits
		chunkSize := 100
		for i := 0; i < len(items); i += chunkSize {
			end := i + chunkSize
			if end > len(items) {
				end = len(items)
			}
			chunk := items[i:end]

			valueStrings := make([]string, 0, len(chunk))
			valueArgs := make([]interface{}, 0, len(chunk)*6)

			for _, item := range chunk {
				item.BatchID = batchID
				item.CreatedAt = now
				item.UpdatedAt = now
				item.Status = ItemStatusPending
				item.IdempotencyKey = fmt.Sprintf("%d-%d", batchID, item.SequenceNo)
				valueStrings = append(valueStrings, "(?, ?, ?, ?, ?, ?)")
				valueArgs = append(valueArgs, batchID, item.SequenceNo, item.IdempotencyKey, ItemStatusPending, now, now)
			}

			stmt := fmt.Sprintf("INSERT INTO batch_order_items (batch_id, sequence_no, idempotency_key, status, created_at, updated_at) VALUES %s",
				strings.Join(valueStrings, ","))

			if _, err := tx.ExecContext(ctx, stmt, valueArgs...); err != nil {
				return 0, fmt.Errorf("failed to insert batch_order_items chunk: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit create batch tx: %w", err)
	}

	return batchID, nil
}

func (r *MySQLRepository) GetBatch(ctx context.Context, batchID int64) (*BatchOrder, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, telegram_user_id, telegram_chat_id, product_code, target_id, qty, status, success_count, failed_count, manual_review_count, created_at, completed_at
		FROM batch_orders WHERE id = ?
	`, batchID)

	var b BatchOrder
	if err := row.Scan(&b.ID, &b.TelegramUserID, &b.TelegramChatID, &b.ProductCode, &b.TargetID, &b.Qty, &b.Status, &b.SuccessCount, &b.FailedCount, &b.ManualReviewCount, &b.CreatedAt, &b.CompletedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan batch_order: %w", err)
	}
	return &b, nil
}

func (r *MySQLRepository) GetProcessingBatches(ctx context.Context) ([]*BatchOrder, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, telegram_user_id, telegram_chat_id, product_code, target_id, qty, status, success_count, failed_count, manual_review_count, created_at, completed_at
		FROM batch_orders WHERE status = ? ORDER BY id ASC
	`, BatchStatusProcessing)
	if err != nil {
		return nil, fmt.Errorf("failed to query processing batches: %w", err)
	}
	defer rows.Close()

	var batches []*BatchOrder
	for rows.Next() {
		var b BatchOrder
		if err := rows.Scan(&b.ID, &b.TelegramUserID, &b.TelegramChatID, &b.ProductCode, &b.TargetID, &b.Qty, &b.Status, &b.SuccessCount, &b.FailedCount, &b.ManualReviewCount, &b.CreatedAt, &b.CompletedAt); err != nil {
			return nil, fmt.Errorf("failed to scan batch row: %w", err)
		}
		batches = append(batches, &b)
	}
	return batches, nil
}

func (r *MySQLRepository) GetBatchItems(ctx context.Context, batchID int64) ([]*BatchOrderItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, batch_id, sequence_no, idempotency_key, sn, provider_ref, status, error_message, created_at, updated_at
		FROM batch_order_items WHERE batch_id = ? ORDER BY sequence_no ASC
	`, batchID)
	if err != nil {
		return nil, fmt.Errorf("failed to query batch items: %w", err)
	}
	defer rows.Close()

	var items []*BatchOrderItem
	for rows.Next() {
		var it BatchOrderItem
		if err := rows.Scan(&it.ID, &it.BatchID, &it.SequenceNo, &it.IdempotencyKey, &it.SN, &it.ProviderRef, &it.Status, &it.ErrorMessage, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan batch item: %w", err)
		}
		items = append(items, &it)
	}
	return items, nil
}

func (r *MySQLRepository) GetPendingItems(ctx context.Context, batchID int64) ([]*BatchOrderItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, batch_id, sequence_no, idempotency_key, sn, provider_ref, status, error_message, created_at, updated_at
		FROM batch_order_items WHERE batch_id = ? AND status = ? ORDER BY sequence_no ASC
	`, batchID, ItemStatusPending)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending items: %w", err)
	}
	defer rows.Close()

	var items []*BatchOrderItem
	for rows.Next() {
		var it BatchOrderItem
		if err := rows.Scan(&it.ID, &it.BatchID, &it.SequenceNo, &it.IdempotencyKey, &it.SN, &it.ProviderRef, &it.Status, &it.ErrorMessage, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan pending item: %w", err)
		}
		items = append(items, &it)
	}
	return items, nil
}

func (r *MySQLRepository) GetInProgressItems(ctx context.Context, batchID int64) ([]*BatchOrderItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, batch_id, sequence_no, idempotency_key, sn, provider_ref, status, error_message, created_at, updated_at
		FROM batch_order_items WHERE batch_id = ? AND status = ? ORDER BY sequence_no ASC
	`, batchID, ItemStatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("failed to query in_progress items: %w", err)
	}
	defer rows.Close()

	var items []*BatchOrderItem
	for rows.Next() {
		var it BatchOrderItem
		if err := rows.Scan(&it.ID, &it.BatchID, &it.SequenceNo, &it.IdempotencyKey, &it.SN, &it.ProviderRef, &it.Status, &it.ErrorMessage, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan in_progress item: %w", err)
		}
		items = append(items, &it)
	}
	return items, nil
}

func (r *MySQLRepository) UpdateItemStatus(ctx context.Context, itemID int64, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE batch_order_items SET status = ?, updated_at = ? WHERE id = ?
	`, status, time.Now(), itemID)
	if err != nil {
		return fmt.Errorf("failed to update item status: %w", err)
	}
	return nil
}

func (r *MySQLRepository) UpdateItemResult(ctx context.Context, itemID int64, status, sn, providerRef, errorMsg string) error {
	var snVal, refVal, errVal sql.NullString
	if sn != "" {
		snVal = sql.NullString{String: sn, Valid: true}
	}
	if providerRef != "" {
		refVal = sql.NullString{String: providerRef, Valid: true}
	}
	if errorMsg != "" {
		errVal = sql.NullString{String: errorMsg, Valid: true}
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE batch_order_items 
		SET status = ?, sn = ?, provider_ref = ?, error_message = ?, updated_at = ?
		WHERE id = ?
	`, status, snVal, refVal, errVal, time.Now(), itemID)
	if err != nil {
		return fmt.Errorf("failed to update item result: %w", err)
	}
	return nil
}

func (r *MySQLRepository) UpdateBatchStatus(ctx context.Context, batchID int64, status string, successCount, failedCount, manualReviewCount int, completedAt *time.Time) error {
	var completedAtVal sql.NullTime
	if completedAt != nil {
		completedAtVal = sql.NullTime{Time: *completedAt, Valid: true}
	}

	_, err := r.db.ExecContext(ctx, `
		UPDATE batch_orders
		SET status = ?, success_count = ?, failed_count = ?, manual_review_count = ?, completed_at = ?
		WHERE id = ?
	`, status, successCount, failedCount, manualReviewCount, completedAtVal, batchID)
	if err != nil {
		return fmt.Errorf("failed to update batch status: %w", err)
	}
	return nil
}

func (r *MySQLRepository) CheckAndCompleteBatch(ctx context.Context, batchID int64) (bool, *BatchOrder, error) {
	// Aggregate status counts for this batch
	row := r.db.QueryRowContext(ctx, `
		SELECT 
			COUNT(CASE WHEN status = 'pending' THEN 1 END) as pending_cnt,
			COUNT(CASE WHEN status = 'in_progress' THEN 1 END) as in_prog_cnt,
			COUNT(CASE WHEN status = 'success' THEN 1 END) as success_cnt,
			COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed_cnt,
			COUNT(CASE WHEN status = 'needs_manual_review' THEN 1 END) as manual_cnt
		FROM batch_order_items
		WHERE batch_id = ?
	`, batchID)

	var pendingCnt, inProgCnt, successCnt, failedCnt, manualCnt int
	if err := row.Scan(&pendingCnt, &inProgCnt, &successCnt, &failedCnt, &manualCnt); err != nil {
		return false, nil, fmt.Errorf("failed to aggregate item statuses: %w", err)
	}

	if pendingCnt == 0 && inProgCnt == 0 {
		// All items are in final state (success, failed, needs_manual_review)
		now := time.Now()
		if err := r.UpdateBatchStatus(ctx, batchID, BatchStatusCompleted, successCnt, failedCnt, manualCnt, &now); err != nil {
			return false, nil, fmt.Errorf("failed to mark batch completed: %w", err)
		}
		updatedBatch, err := r.GetBatch(ctx, batchID)
		return true, updatedBatch, err
	}

	// Not all items are completed yet, but update intermediate counts
	_ = r.UpdateBatchStatus(ctx, batchID, BatchStatusProcessing, successCnt, failedCnt, manualCnt, nil)
	currentBatch, err := r.GetBatch(ctx, batchID)
	return false, currentBatch, err
}
