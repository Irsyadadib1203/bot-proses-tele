package core

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"bot-proses/internal/store"
)

func TestGenerateRecapXLSXAndCaption(t *testing.T) {
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

	xlsxBytes, filename, err := GenerateRecapXLSX(batch, items)
	if err != nil {
		t.Fatalf("GenerateRecapXLSX failed: %v", err)
	}

	if !strings.HasPrefix(filename, "rekap_batch_10_FF5_") || !strings.HasSuffix(filename, ".xlsx") {
		t.Errorf("unexpected filename format: %s", filename)
	}

	f, err := excelize.OpenReader(bytes.NewReader(xlsxBytes))
	if err != nil {
		t.Fatalf("failed to open generated xlsx: %v", err)
	}
	defer f.Close()

	sheetName := "Rekap Batch"
	rows, err := f.GetRows(sheetName)
	if err != nil {
		t.Fatalf("failed to get rows from sheet %s: %v", sheetName, err)
	}

	if len(rows) < 3 {
		t.Fatalf("expected at least 3 rows (1 header + 2 items), got %d rows", len(rows))
	}

	// Verify Header
	if rows[0][0] != "No Urut" || rows[0][5] != "Serial Number (SN)" {
		t.Errorf("unexpected header values: %v", rows[0])
	}

	// Verify Item 1 (Success)
	if rows[1][0] != "1" || rows[1][5] != "SN-12345" || rows[1][4] != string(store.ItemStatusSuccess) {
		t.Errorf("unexpected item 1 values: %v", rows[1])
	}

	// Verify Item 2 (Failed)
	if rows[2][0] != "2" || rows[2][7] != "Out of stock" || rows[2][4] != string(store.ItemStatusFailed) {
		t.Errorf("unexpected item 2 values: %v", rows[2])
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
	if !strings.Contains(caption, "Excel (XLSX)") {
		t.Errorf("caption should mention Excel (XLSX), got: %s", caption)
	}
}

func TestGenerateRecapCSVBackwardCompatibility(t *testing.T) {
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
	}

	csvBytes, filename, err := GenerateRecapCSV(batch, items)
	if err != nil {
		t.Fatalf("GenerateRecapCSV failed: %v", err)
	}
	if !strings.HasSuffix(filename, ".csv") {
		t.Errorf("expected .csv suffix, got: %s", filename)
	}
	if !strings.Contains(string(csvBytes), "SN-12345") {
		t.Errorf("expected SN-12345 in CSV, got: %s", string(csvBytes))
	}
}
