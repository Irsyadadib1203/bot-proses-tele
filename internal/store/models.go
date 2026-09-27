package store

import (
	"database/sql"
	"time"
)

const (
	BatchStatusProcessing = "processing"
	BatchStatusCompleted  = "completed"

	ItemStatusPending           = "pending"
	ItemStatusInProgress        = "in_progress"
	ItemStatusSuccess           = "success"
	ItemStatusFailed            = "failed"
	ItemStatusNeedsManualReview = "needs_manual_review"
)

type BatchOrder struct {
	ID                int64        `json:"id"`
	TelegramUserID    int64        `json:"telegram_user_id"`
	TelegramChatID    int64        `json:"telegram_chat_id"`
	ProductCode       string       `json:"product_code"`
	TargetID          string       `json:"target_id"`
	Qty               int          `json:"qty"`
	Status            string       `json:"status"`
	SuccessCount      int          `json:"success_count"`
	FailedCount       int          `json:"failed_count"`
	ManualReviewCount int          `json:"manual_review_count"`
	CreatedAt         time.Time    `json:"created_at"`
	CompletedAt       sql.NullTime `json:"completed_at"`
}

type BatchOrderItem struct {
	ID             int64          `json:"id"`
	BatchID        int64          `json:"batch_id"`
	SequenceNo     int            `json:"sequence_no"`
	IdempotencyKey string         `json:"idempotency_key"`
	SN             sql.NullString `json:"sn"`
	ProviderRef    sql.NullString `json:"provider_ref"`
	Status         string         `json:"status"`
	ErrorMessage   sql.NullString `json:"error_message"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}
