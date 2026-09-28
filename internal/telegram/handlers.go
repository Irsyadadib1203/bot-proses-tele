package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"bot-proses/internal/core"
	"bot-proses/internal/store"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (s *BotService) handleMessage(msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if text == "" {
		return
	}

	// 1. Whitelist validation
	if !s.cfg.IsUserAllowed(userID) {
		s.logger.Warn("unauthorized user access attempt", "user_id", userID, "chat_id", chatID, "username", msg.From.UserName)
		s.sendTextMessage(chatID, fmt.Sprintf("⛔ **Akses Ditolak**\nID Telegram Anda (`%d`) belum terdaftar di whitelist bot ini.\nHubungi administrator untuk meminta akses.", userID))
		return
	}

	// 2. Route commands
	if strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/help") {
		s.handleHelp(chatID)
		return
	}

	if strings.HasPrefix(text, "/status") || strings.HasPrefix(text, "/cekstatus") || strings.HasPrefix(text, "/checkstatus") {
		s.handleStatus(chatID, text)
		return
	}

	// 3. Check for BUY command
	if strings.HasPrefix(strings.ToUpper(text), "BUY") || strings.HasPrefix(strings.ToUpper(text), "/BUY") {
		s.handleBuy(msg)
		return
	}

	// Default fallback
	s.sendTextMessage(chatID, "ℹ️ Perintah tidak dikenali.\n\nGunakan format:\n`BUY <KODE_PRODUK> <TARGET_ID> <QTY>`\n\nKetik `/help` untuk bantuan.")
}

func (s *BotService) handleHelp(chatID int64) {
	var productList strings.Builder
	if len(s.cfg.Products) > 0 {
		productList.WriteString("\n📋 **Daftar Kode Produk Tersedia:**\n")
		for code := range s.cfg.Products {
			productList.WriteString(fmt.Sprintf("• `%s`\n", code))
		}
	}

	helpText := fmt.Sprintf(`🤖 **Bulk Order Bot**

Gunakan bot ini untuk memproses pembelian produk secara massal.

📌 **Format Perintah:**
`+"`BUY <KODE_PRODUK> <TARGET_ID> <QTY>`"+`

Contoh:
`+"`BUY FF5 1267876327 100`"+`

⚙️ **Batasan & Konfigurasi:**
• Maksimal per batch: %d item
• Concurrency worker: %d parallel
%s
🔍 Ketik `+"`/status`"+` untuk melihat batch aktif, atau `+"`/status <BATCH_ID>`"+` untuk mengecek dan sinkronisasi status batch dari provider.`,
		s.cfg.Worker.MaxQtyPerBatch,
		s.cfg.Worker.Concurrency,
		productList.String(),
	)

	s.sendTextMessage(chatID, helpText)
}

func (s *BotService) handleStatus(chatID int64, text string) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		batches, err := s.repo.GetProcessingBatches(ctx)
		if err != nil {
			s.sendTextMessage(chatID, fmt.Sprintf("⚠️ Gagal mengambil daftar batch: %v", err))
			return
		}

		if len(batches) == 0 {
			s.sendTextMessage(chatID, "ℹ️ Tidak ada batch yang sedang berjalan saat ini.\n\nGunakan format:\n`/status <BATCH_ID>` untuk mengecek batch tertentu.")
			return
		}

		var sb strings.Builder
		sb.WriteString("📋 **Batch yang Sedang Berjalan:**\n\n")
		for _, b := range batches {
			sb.WriteString(fmt.Sprintf("• **Batch #%d** | Produk: `%s` | Target: `%s` | Qty: %d\n  Status: `%s` (Sukses: %d, Gagal: %d)\n",
				b.ID, b.ProductCode, b.TargetID, b.Qty, b.Status, b.SuccessCount, b.FailedCount))
		}
		sb.WriteString("\n🔍 Ketik `/status <BATCH_ID>` untuk memperbarui status langsung dari provider.")
		s.sendTextMessage(chatID, sb.String())
		return
	}

	batchID, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		s.sendTextMessage(chatID, "⚠️ ID batch tidak valid, harus berupa angka.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	batch, err := s.repo.GetBatch(ctx, batchID)
	if err != nil {
		s.sendTextMessage(chatID, fmt.Sprintf("⚠️ Gagal mengambil status batch: %v", err))
		return
	}

	if batch == nil {
		s.sendTextMessage(chatID, fmt.Sprintf("⚠️ Batch #%d tidak ditemukan.", batchID))
		return
	}

	// If batch is currently processing, sync status on-demand with provider
	if batch.Status == store.BatchStatusProcessing && s.poller != nil {
		s.sendTextMessage(chatID, fmt.Sprintf("🔄 Mengecek status terbaru Batch #%d ke provider...", batchID))
		if syncedBatch, syncErr := s.poller.SyncBatch(ctx, batchID); syncErr == nil && syncedBatch != nil {
			batch = syncedBatch
		} else if syncErr != nil {
			s.logger.Warn("on-demand sync batch error", "batch_id", batchID, "err", syncErr)
		}
	}

	statusEmoji := "⏳"
	if batch.Status == store.BatchStatusCompleted {
		statusEmoji = "✅"
	}

	statusMsg := fmt.Sprintf("📊 **Status Batch #%d** %s\n\n"+
		"• Status: `%s`\n"+
		"• Produk: `%s`\n"+
		"• Target ID: `%s`\n"+
		"• Total Order: %d\n"+
		"• Sukses: %d\n"+
		"• Gagal: %d\n"+
		"• Review Manual: %d\n"+
		"• Dibuat: %s",
		batch.ID,
		statusEmoji,
		batch.Status,
		batch.ProductCode,
		batch.TargetID,
		batch.Qty,
		batch.SuccessCount,
		batch.FailedCount,
		batch.ManualReviewCount,
		batch.CreatedAt.Format("2006-01-02 15:04:05"),
	)

	if batch.Status == store.BatchStatusProcessing {
		statusMsg += "\n\n💡 *Batch masih diproses oleh provider. Bot akan otomatis menyelesaikan dan mengirim rekap XLSX saat seluruh order tuntas.*"
	}

	s.sendTextMessage(chatID, statusMsg)
}

func (s *BotService) handleBuy(msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := msg.Text

	// Rate limit check
	if !s.rateLimiter.Allow(userID) {
		s.logger.Warn("rate limit exceeded for user", "user_id", userID)
		s.sendTextMessage(chatID, "⚠️ **Terlalu Banyak Permintaan**\nSilakan tunggu beberapa saat sebelum mengirim batch order baru.")
		return
	}

	// Parse command
	parsed, err := core.ParseBuyCommand(text, s.cfg.Worker.MaxQtyPerBatch)
	if err != nil {
		s.sendTextMessage(chatID, fmt.Sprintf("⚠️ %s", err.Error()))
		return
	}

	// Build batch order and items
	batch := &store.BatchOrder{
		TelegramUserID: userID,
		TelegramChatID: chatID,
		ProductCode:    parsed.ProductCode,
		TargetID:       parsed.TargetID,
		Qty:            parsed.Qty,
	}

	items := make([]*store.BatchOrderItem, parsed.Qty)
	for i := 1; i <= parsed.Qty; i++ {
		items[i-1] = &store.BatchOrderItem{
			SequenceNo:     i,
			IdempotencyKey: fmt.Sprintf("%d-%d", 0, i), // Will be updated with actual batchID during/after insert
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	batchID, err := s.repo.CreateBatch(ctx, batch, items)
	if err != nil {
		s.logger.Error("failed to create batch in database", "err", err)
		s.sendTextMessage(chatID, fmt.Sprintf("❌ Terjadi kesalahan saat membuat batch order: %v", err))
		return
	}

	// Update idempotency key properly with real batchID
	for _, item := range items {
		item.BatchID = batchID
		item.IdempotencyKey = fmt.Sprintf("%d-%d", batchID, item.SequenceNo)
	}

	s.logger.Info("batch created successfully", "batch_id", batchID, "qty", parsed.Qty, "product", parsed.ProductCode)

	// Send immediate acknowledgement response
	initialMsg := fmt.Sprintf("⏳ **Batch #%d diterima, memproses %d order...**\n\nProduk: `%s` | Target: `%s`",
		batchID, parsed.Qty, parsed.ProductCode, parsed.TargetID)
	s.sendTextMessage(chatID, initialMsg)

	// Asynchronously trigger background processing (non-blocking)
	s.orchestrator.EnqueueBatch(batch, items)
}
