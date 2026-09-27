package core

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"bot-proses/internal/store"
)

func TestGenerateRecapCSVAndCaption(t *testing.T) {
	batch := &store.BatchOrder{
		ID:                10,
		TelegramUserID:    12345,
		TelegramChatID:    67890,
		ProductCode:       "FF5",
		TargetID:          "11223344",
		Qty:               2,
		Status:            store.BatchStatusCompleted,
		SuccessCount:      1,
		FailedCount:       1,
		ManualReviewCount: 0,
		CreatedAt:         time.Now(),
	}

	items := []*store.BatchOrderItem{
		{
			ID:             101,
			BatchID:        10,
			SequenceNo:     1,
			IdempotencyKey: "10-1",
			SN:             sql.NullString{String: "SN-12345", Valid: true},
			ProviderRef:    sql.NullString{String: "REF-101", Valid: true},
			Status:         store.ItemStatusSuccess,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
		{
			ID:             102,
			BatchID:        10,
			SequenceNo:     2,
			IdempotencyKey: "10-2",
			Status:         store.ItemStatusFailed,
			ErrorMessage:   sql.NullString{String: "Out of stock", Valid: true},
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		},
	}

	csvBytes, filename, err := GenerateRecapCSV(batch, items)
	if err != nil {
		t.Fatalf("GenerateRecapCSV failed: %v", err)
	}

	if !strings.HasPrefix(filename, "rekap_batch_10_FF5_") || !strings.HasSuffix(filename, ".csv") {
		t.Errorf("unexpected filename format: %s", filename)
	}

	csvStr := string(csvBytes)
	if !strings.Contains(csvStr, "SN-12345") {
		t.Errorf("CSV should contain serial number SN-12345, got: %s", csvStr)
	}
	if !strings.Contains(csvStr, "Out of stock") {
		t.Errorf("CSV should contain error message 'Out of stock', got: %s", csvStr)
	}

	caption := FormatRecapCaption(batch)
	if !strings.Contains(caption, "Batch #10") {
		t.Errorf("caption should contain Batch #10, got: %s", caption)
	}
	if !strings.Contains(caption, "**Sukses:** 1") {
		t.Errorf("caption should contain **Sukses:** 1, got: %s", caption)
	}
	if !strings.Contains(caption, "**Gagal:** 1") {
		t.Errorf("caption should contain **Gagal:** 1, got: %s", caption)
	}
}
