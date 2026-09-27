package core

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsedBuyCommand holds the validated fields of a BUY command.
type ParsedBuyCommand struct {
	ProductCode string
	TargetID    string
	Qty         int
}

// ParseBuyCommand parses and validates the text of a BUY command.
// Expected format: BUY <KODE_PRODUK> <TARGET_ID> <QTY>
func ParseBuyCommand(text string, maxQty int) (*ParsedBuyCommand, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("pesan kosong. Gunakan format: BUY <KODE_PRODUK> <TARGET_ID> <QTY>\nContoh: BUY FF5 1267876327 100")
	}

	fields := strings.Fields(text)
	if len(fields) < 4 {
		return nil, fmt.Errorf("format salah!\nGunakan format: BUY <KODE_PRODUK> <TARGET_ID> <QTY>\nContoh: BUY FF5 1267876327 100")
	}

	cmd := strings.ToUpper(fields[0])
	if cmd != "BUY" && cmd != "/BUY" {
		return nil, fmt.Errorf("perintah tidak dikenali: %s. Gunakan perintah BUY", fields[0])
	}

	productCode := strings.TrimSpace(fields[1])
	if productCode == "" {
		return nil, fmt.Errorf("kode produk tidak boleh kosong")
	}

	targetID := strings.TrimSpace(fields[2])
	if targetID == "" {
		return nil, fmt.Errorf("target ID / No Tujuan tidak boleh kosong")
	}

	rawQty := strings.TrimSpace(fields[3])
	qty, err := strconv.Atoi(rawQty)
	if err != nil {
		return nil, fmt.Errorf("jumlah order (QTY) '%s' tidak valid, harus berupa angka bulat positif", rawQty)
	}

	if qty <= 0 {
		return nil, fmt.Errorf("jumlah order (QTY) harus lebih besar dari 0")
	}

	if maxQty > 0 && qty > maxQty {
		return nil, fmt.Errorf("jumlah order (QTY) %d melebihi batas maksimum per batch (%d item)", qty, maxQty)
	}

	return &ParsedBuyCommand{
		ProductCode: productCode,
		TargetID:    targetID,
		Qty:         qty,
	}, nil
}
