package core

import (
	"testing"
)

func TestParseBuyCommand(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		maxQty      int
		wantErr     bool
		wantProduct string
		wantTarget  string
		wantQty     int
	}{
		{
			name:        "Valid command standard",
			input:       "BUY FF5 1267876327 100",
			maxQty:      200,
			wantErr:     false,
			wantProduct: "FF5",
			wantTarget:  "1267876327",
			wantQty:     100,
		},
		{
			name:        "Valid lowercase buy and multiple spaces",
			input:       "buy   ML10   user123_zone456   50  ",
			maxQty:      200,
			wantErr:     false,
			wantProduct: "ML10",
			wantTarget:  "user123_zone456",
			wantQty:     50,
		},
		{
			name:        "Valid /buy with slash",
			input:       "/buy FF50 998877 10",
			maxQty:      200,
			wantErr:     false,
			wantProduct: "FF50",
			wantTarget:  "998877",
			wantQty:     10,
		},
		{
			name:    "Invalid missing args",
			input:   "BUY FF5 123",
			maxQty:  200,
			wantErr: true,
		},
		{
			name:    "Invalid non-integer qty",
			input:   "BUY FF5 123 abc",
			maxQty:  200,
			wantErr: true,
		},
		{
			name:    "Invalid zero qty",
			input:   "BUY FF5 123 0",
			maxQty:  200,
			wantErr: true,
		},
		{
			name:    "Invalid negative qty",
			input:   "BUY FF5 123 -5",
			maxQty:  200,
			wantErr: true,
		},
		{
			name:    "Exceeds max qty",
			input:   "BUY FF5 123 250",
			maxQty:  200,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBuyCommand(tt.input, tt.maxQty)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseBuyCommand() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if got.ProductCode != tt.wantProduct {
					t.Errorf("ProductCode = %v, want %v", got.ProductCode, tt.wantProduct)
				}
				if got.TargetID != tt.wantTarget {
					t.Errorf("TargetID = %v, want %v", got.TargetID, tt.wantTarget)
				}
				if got.Qty != tt.wantQty {
					t.Errorf("Qty = %v, want %v", got.Qty, tt.wantQty)
				}
			}
		})
	}
}
