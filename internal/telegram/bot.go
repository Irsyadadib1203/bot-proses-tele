package telegram

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"bot-proses/config"
	"bot-proses/internal/core"
	"bot-proses/internal/store"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type BotService struct {
	bot          *tgbotapi.BotAPI
	cfg          *config.Config
	repo         store.Repository
	orchestrator *core.Orchestrator
	poller       *core.StatusPoller
	rateLimiter  *core.UserRateLimiter
	logger       *slog.Logger
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

func NewBotService(
	cfg *config.Config,
	repo store.Repository,
	orchestrator *core.Orchestrator,
	poller *core.StatusPoller,
	logger *slog.Logger,
) (*BotService, error) {
	if logger == nil {
		logger = slog.Default()
	}

	bot, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize telegram bot api: %w", err)
	}

	rateLimitReqs := cfg.Telegram.RateLimitPerUser
	if rateLimitReqs <= 0 {
		rateLimitReqs = 10
	}
	rateLimiter := core.NewUserRateLimiter(rateLimitReqs, 1*time.Minute)

	svc := &BotService{
		bot:          bot,
		cfg:          cfg,
		repo:         repo,
		orchestrator: orchestrator,
		poller:       poller,
		rateLimiter:  rateLimiter,
		logger:       logger,
		stopCh:       make(chan struct{}),
	}

	return svc, nil
}

// Start begins the Telegram update polling loop.
func (s *BotService) Start() {
	s.logger.Info("telegram bot started", "username", s.bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := s.bot.GetUpdatesChan(u)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.stopCh:
				s.logger.Info("stopping telegram updates loop")
				s.bot.StopReceivingUpdates()
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				if update.Message != nil {
					s.handleMessage(update.Message)
				}
			}
		}
	}()
}

// Stop gracefully terminates the polling loop.
func (s *BotService) Stop() {
	close(s.stopCh)
	s.wg.Wait()
	s.logger.Info("telegram bot stopped")
}

// SendBatchRecap sends the completed batch CSV file and summary caption to the user chat.
func (s *BotService) SendBatchRecap(batch *store.BatchOrder, items []*store.BatchOrderItem) {
	s.logger.Info("sending batch recap to telegram", "batch_id", batch.ID, "chat_id", batch.TelegramChatID)

	csvData, filename, err := core.GenerateRecapCSV(batch, items)
	if err != nil {
		s.logger.Error("failed to generate recap csv", "batch_id", batch.ID, "err", err)
		s.sendTextMessage(batch.TelegramChatID, fmt.Sprintf("⚠️ Gagal membuat file CSV untuk Batch #%d: %v", batch.ID, err))
		return
	}

	caption := core.FormatRecapCaption(batch)

	fileBytes := tgbotapi.FileBytes{
		Name:  filename,
		Bytes: csvData,
	}

	docMsg := tgbotapi.NewDocument(batch.TelegramChatID, fileBytes)
	docMsg.Caption = caption
	docMsg.ParseMode = tgbotapi.ModeMarkdown

	if _, err := s.bot.Send(docMsg); err != nil {
		s.logger.Error("failed to send telegram document", "batch_id", batch.ID, "err", err)
		// Fallback to text message if document sending fails
		s.sendTextMessage(batch.TelegramChatID, caption+"\n\n⚠️ Catatan: Pengiriman file rekap CSV gagal.")
	} else {
		s.logger.Info("batch recap sent successfully", "batch_id", batch.ID)
	}
}

func (s *BotService) sendTextMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := s.bot.Send(msg); err != nil {
		// Fallback without parse mode in case markdown formatting fails
		msg.ParseMode = ""
		_, _ = s.bot.Send(msg)
	}
}
