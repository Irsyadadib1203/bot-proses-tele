package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bot-proses/config"
	"bot-proses/internal/adapter"
	"bot-proses/internal/core"
	"bot-proses/internal/store"
	"bot-proses/internal/telegram"
	"bot-proses/migrations"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migmysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "Path to config file")
	flag.Parse()

	// 1. Structured Logging setup
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	logger.Info("starting Telegram Bulk Order Bot...")

	// 2. Load Configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		logger.Error("failed to load configuration", "err", err)
		os.Exit(1)
	}

	if cfg.Telegram.BotToken == "" {
		logger.Error("TELEGRAM_BOT_TOKEN is required. Please set it in .env or config.yaml")
		os.Exit(1)
	}

	// 3. Initialize Database Connection Pool
	db, err := sql.Open("mysql", cfg.Database.DSN())
	if err != nil {
		logger.Error("failed to open database connection", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(cfg.Database.ConnMaxLifetime) * time.Second)

	// Verify database connection
	ctxPing, cancelPing := context.WithTimeout(context.Background(), 10*time.Second)
	if err := db.PingContext(ctxPing); err != nil {
		cancelPing()
		logger.Error("failed to ping database, check your connection string", "err", err)
		os.Exit(1)
	}
	cancelPing()
	logger.Info("database connection established successfully")

	// 4. Run Automatic Database Migrations via golang-migrate
	if err := runMigrations(db, logger); err != nil {
		logger.Error("database migration failed", "err", err)
		os.Exit(1)
	}

	// 5. Initialize Layers (Store, Adapter, Core, Telegram)
	repo := store.NewMySQLRepository(db)
	productAdapter := adapter.NewDefaultAdapter(cfg.Target, cfg.Products, logger)

	var botService *telegram.BotService

	// Completion callback sends CSV recap to Telegram
	completionCallback := func(batch *store.BatchOrder, items []*store.BatchOrderItem) {
		if botService != nil {
			botService.SendBatchRecap(batch, items)
		}
	}

	orchestrator := core.NewOrchestrator(
		repo,
		productAdapter,
		cfg.Worker.Concurrency,
		completionCallback,
		logger,
	)

	botService, err = telegram.NewBotService(cfg, repo, orchestrator, logger)
	if err != nil {
		logger.Error("failed to initialize telegram bot service", "err", err)
		os.Exit(1)
	}

	// 6. Resume Interrupted Batches (Recovery on Startup)
	resumeMgr := core.NewResumeManager(repo, productAdapter, orchestrator, logger)
	ctxResume, cancelResume := context.WithTimeout(context.Background(), 60*time.Second)
	if err := resumeMgr.ResumeUnfinishedBatches(ctxResume); err != nil {
		logger.Error("error while resuming unfinished batches", "err", err)
	}
	cancelResume()

	// 7. Start Telegram Bot Polling
	botService.Start()
	logger.Info("system is ready and listening for Telegram commands")

	// 8. Graceful Shutdown Listener
	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	sig := <-stopSignal
	logger.Info("received termination signal, initiating graceful shutdown...", "signal", sig.String())

	// Step A: Stop receiving new Telegram updates
	botService.Stop()

	// Step B: Stop orchestrator dispatching and wait for in-flight workers to finish HTTP calls & DB saves
	shutdownTimeout := time.Duration(cfg.Worker.ShutdownTimeoutSec) * time.Second
	if shutdownTimeout <= 0 {
		shutdownTimeout = 30 * time.Second
	}
	orchestrator.Stop(shutdownTimeout)

	// Step C: Close database
	if err := db.Close(); err != nil {
		logger.Error("error closing database", "err", err)
	}

	logger.Info("application stopped cleanly")
}

func runMigrations(db *sql.DB, logger *slog.Logger) error {
	logger.Info("running database migrations...")

	d, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("failed to create iofs migration driver: %w", err)
	}

	driver, err := migmysql.WithInstance(db, &migmysql.Config{})
	if err != nil {
		return fmt.Errorf("failed to create mysql migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", d, "mysql", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	logger.Info("database migrations applied successfully")
	return nil
}
