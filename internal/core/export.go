package core

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"time"

	"github.com/xuri/excelize/v2"

	"bot-proses/internal/store"
)

// GenerateRecapXLSX builds an Excel (.xlsx) report for a completed or processed batch.
func GenerateRecapXLSX(batch *store.BatchOrder, items []*store.BatchOrderItem) ([]byte, string, error) {
	f := excelize.NewFile()
	defer func() {
		_ = f.Close()
	}()

	sheetName := "Rekap Batch"
	f.SetSheetName("Sheet1", sheetName)

	// Header style: Navy background, white bold text, centered, light borders
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{
			Bold:  true,
			Color: "FFFFFF",
			Size:  11,
		},
		Fill: excelize.Fill{
			Type:    "pattern",
			Color:   []string{"#1F497D"},
			Pattern: 1,
		},
		Alignment: &excelize.Alignment{
			Horizontal: "center",
			Vertical:   "center",
		},
		Border: []excelize.Border{
			{Type: "top", Style: 1, Color: "D9D9D9"},
			{Type: "bottom", Style: 1, Color: "D9D9D9"},
			{Type: "left", Style: 1, Color: "D9D9D9"},
			{Type: "right", Style: 1, Color: "D9D9D9"},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to create header style: %w", err)
	}

	// Normal data row style
	dataStyle, err := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{
			Vertical: "center",
		},
		Border: []excelize.Border{
			{Type: "top", Style: 1, Color: "E0E0E0"},
			{Type: "bottom", Style: 1, Color: "E0E0E0"},
			{Type: "left", Style: 1, Color: "E0E0E0"},
			{Type: "right", Style: 1, Color: "E0E0E0"},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to create data style: %w", err)
	}

	// Centered data row style
	dataCenterStyle, err := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{
			Horizontal: "center",
			Vertical:   "center",
		},
		Border: []excelize.Border{
			{Type: "top", Style: 1, Color: "E0E0E0"},
			{Type: "bottom", Style: 1, Color: "E0E0E0"},
			{Type: "left", Style: 1, Color: "E0E0E0"},
			{Type: "right", Style: 1, Color: "E0E0E0"},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to create data center style: %w", err)
	}

	headers := []string{
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

	for colIdx, h := range headers {
		cell, err := excelize.CoordinatesToCellName(colIdx+1, 1)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get cell name: %w", err)
		}
		_ = f.SetCellValue(sheetName, cell, h)
		_ = f.SetCellStyle(sheetName, cell, cell, headerStyle)
	}
	_ = f.SetRowHeight(sheetName, 1, 26)

	for rowIdx, item := range items {
		rowNum := rowIdx + 2

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

		_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), item.SequenceNo)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), item.BatchID)
		_ = f.SetCellStr(sheetName, fmt.Sprintf("C%d", rowNum), item.IdempotencyKey)
		_ = f.SetCellStr(sheetName, fmt.Sprintf("D%d", rowNum), batch.TargetID)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), item.Status)
		_ = f.SetCellStr(sheetName, fmt.Sprintf("F%d", rowNum), sn)
		_ = f.SetCellStr(sheetName, fmt.Sprintf("G%d", rowNum), ref)
		_ = f.SetCellStr(sheetName, fmt.Sprintf("H%d", rowNum), errMsg)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("I%d", rowNum), item.CreatedAt.Format("2006-01-02 15:04:05"))
		_ = f.SetCellValue(sheetName, fmt.Sprintf("J%d", rowNum), item.UpdatedAt.Format("2006-01-02 15:04:05"))

		_ = f.SetCellStyle(sheetName, fmt.Sprintf("A%d", rowNum), fmt.Sprintf("B%d", rowNum), dataCenterStyle)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("C%d", rowNum), fmt.Sprintf("D%d", rowNum), dataStyle)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("E%d", rowNum), fmt.Sprintf("E%d", rowNum), dataCenterStyle)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("F%d", rowNum), fmt.Sprintf("H%d", rowNum), dataStyle)
		_ = f.SetCellStyle(sheetName, fmt.Sprintf("I%d", rowNum), fmt.Sprintf("J%d", rowNum), dataCenterStyle)

		_ = f.SetRowHeight(sheetName, rowNum, 20)
	}

	colWidths := map[string]float64{
		"A": 10,
		"B": 12,
		"C": 24,
		"D": 20,
		"E": 14,
		"F": 25,
		"G": 20,
		"H": 30,
		"I": 22,
		"J": 22,
	}
	for col, width := range colWidths {
		_ = f.SetColWidth(sheetName, col, col, width)
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, "", fmt.Errorf("failed to write XLSX to buffer: %w", err)
	}

	filename := fmt.Sprintf("rekap_batch_%d_%s_%s.xlsx", batch.ID, batch.ProductCode, time.Now().Format("20060102_150405"))
	return buf.Bytes(), filename, nil
}

// GenerateRecapCSV builds a CSV report for a completed or processed batch (kept for backward compatibility).
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

	caption += "\n📄 Detail tiap item tersimpan dalam file Excel (XLSX) terlampir."
	return caption
}
