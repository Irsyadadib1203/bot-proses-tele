package core

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"time"

	"bot-proses/internal/store"
)

// GenerateRecapCSV builds a CSV report for a completed or processed batch.
func GenerateRecapCSV(batch *store.BatchOrder, items []*store.BatchOrderItem) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	// CSV Header
	header := []string{
		"No Urut",
		"Batch ID",
		"Idempotency Key",
		"Target ID",
		"Status",
		"Serial Number (SN)",
		"Provider Ref",
		"Pesan Error",
		"Waktu Dibuat",
		"Waktu Selesai",
	}
	if err := writer.Write(header); err != nil {
		return nil, "", fmt.Errorf("failed to write CSV header: %w", err)
	}

	for _, item := range items {
		sn := ""
		if item.SN.Valid {
			sn = item.SN.String
		}
		ref := ""
		if item.ProviderRef.Valid {
			ref = item.ProviderRef.String
		}
		errMsg := ""
		if item.ErrorMessage.Valid {
			errMsg = item.ErrorMessage.String
		}

		row := []string{
			strconv.Itoa(item.SequenceNo),
			strconv.FormatInt(item.BatchID, 10),
			item.IdempotencyKey,
			batch.TargetID,
			item.Status,
			sn,
			ref,
			errMsg,
			item.CreatedAt.Format("2006-01-02 15:04:05"),
			item.UpdatedAt.Format("2006-01-02 15:04:05"),
		}
		if err := writer.Write(row); err != nil {
			return nil, "", fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, "", fmt.Errorf("failed to flush CSV writer: %w", err)
	}

	filename := fmt.Sprintf("rekap_batch_%d_%s_%s.csv", batch.ID, batch.ProductCode, time.Now().Format("20060102_150405"))
	return buf.Bytes(), filename, nil
}

// FormatRecapCaption creates a user-friendly Telegram caption for the recap document.
func FormatRecapCaption(batch *store.BatchOrder) string {
	caption := fmt.Sprintf("✅ **Batch #%d Selesai Diproses**\n\n", batch.ID)
	caption += fmt.Sprintf("📦 **Produk:** `%s`\n", batch.ProductCode)
	caption += fmt.Sprintf("🎯 **Target ID:** `%s`\n", batch.TargetID)
	caption += fmt.Sprintf("📊 **Total Order:** %d\n", batch.Qty)
	caption += fmt.Sprintf("🟢 **Sukses:** %d\n", batch.SuccessCount)
	caption += fmt.Sprintf("🔴 **Gagal:** %d\n", batch.FailedCount)

	if batch.ManualReviewCount > 0 {
		caption += fmt.Sprintf("⚠️ **Perlu Review Manual:** %d\n", batch.ManualReviewCount)
	}

	caption += "\n📄 Detail tiap item tersimpan dalam file CSV terlampir."
	return caption
}
