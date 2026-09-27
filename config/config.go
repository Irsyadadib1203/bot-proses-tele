package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram    TelegramConfig    `yaml:"telegram"`
	Target      TargetConfig      `yaml:"target"`
	Database    DatabaseConfig    `yaml:"database"`
	Worker      WorkerConfig      `yaml:"worker"`
	Products    map[string]string `yaml:"products"`
	WebhookPort string            `yaml:"webhook_port"`
}

type TelegramConfig struct {
	BotToken          string  `yaml:"bot_token"`
	AllowedUserIDs    []int64 `yaml:"allowed_user_ids"`
	RateLimitPerUser  int     `yaml:"rate_limit_per_minute"` // max BUY commands per minute per user
}

type TargetConfig struct {
	BaseURL     string `yaml:"base_url"`
	APIKey      string `yaml:"api_key"`
	APISecret   string `yaml:"api_secret"`
	CallbackURL string `yaml:"callback_url"`
	TimeoutMs   int    `yaml:"timeout_ms"`
}

type DatabaseConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	User            string `yaml:"user"`
	Password        string `yaml:"password"`
	DBName          string `yaml:"dbname"`
	MaxOpenConns    int    `yaml:"max_open_conns"`
	MaxIdleConns    int    `yaml:"max_idle_conns"`
	ConnMaxLifetime int    `yaml:"conn_max_lifetime_seconds"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&multiStatements=true&loc=Local",
		d.User, d.Password, d.Host, d.Port, d.DBName)
}

type WorkerConfig struct {
	Concurrency        int `yaml:"concurrency"`           // default: 5
	MaxQtyPerBatch     int `yaml:"max_qty_per_batch"`     // default: 200
	ShutdownTimeoutSec int `yaml:"shutdown_timeout_sec"`   // default: 30
	PollIntervalSec    int `yaml:"poll_interval_sec"`      // default: 30 (periodic status poller)
}

// LoadConfig reads config from yaml file and overrides with environment variables (.env / OS env)
func LoadConfig(yamlPath string) (*Config, error) {
	// Try to load .env file if present
	_ = godotenv.Load()

	cfg := &Config{
		Worker: WorkerConfig{
			Concurrency:        5,
			MaxQtyPerBatch:     200,
			ShutdownTimeoutSec: 30,
			PollIntervalSec:    30,
		},
		Telegram: TelegramConfig{
			RateLimitPerUser: 10,
		},
		Database: DatabaseConfig{
			Host:            "127.0.0.1",
			Port:            3306,
			User:            "root",
			Password:        "",
			DBName:          "bulk_order_db",
			MaxOpenConns:    25,
			MaxIdleConns:    10,
			ConnMaxLifetime: 300,
		},
		Products:    make(map[string]string),
		WebhookPort: "8080",
	}

	if yamlPath != "" {
		if data, err := os.ReadFile(yamlPath); err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("failed to parse yaml config: %w", err)
			}
		}
	}

	// Override with environment variables if provided
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		cfg.Telegram.BotToken = token
	}

	if allowedIDs := os.Getenv("ALLOWED_TELEGRAM_USER_IDS"); allowedIDs != "" {
		cfg.Telegram.AllowedUserIDs = parseAllowedIDs(allowedIDs)
	}

	if url := os.Getenv("TARGET_BASE_URL"); url != "" {
		cfg.Target.BaseURL = url
	}
	if apiKey := os.Getenv("TARGET_API_KEY"); apiKey != "" {
		cfg.Target.APIKey = apiKey
	}
	if apiSecret := os.Getenv("TARGET_API_SECRET"); apiSecret != "" {
		cfg.Target.APISecret = apiSecret
	}
	if cbURL := os.Getenv("TARGET_CALLBACK_URL"); cbURL != "" {
		cfg.Target.CallbackURL = cbURL
	} else if cbURL := os.Getenv("CALLBACK_URL"); cbURL != "" {
		cfg.Target.CallbackURL = cbURL
	}

	if host := os.Getenv("DB_HOST"); host != "" {
		cfg.Database.Host = host
	}
	if port := os.Getenv("DB_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			cfg.Database.Port = p
		}
	}
	if user := os.Getenv("DB_USER"); user != "" {
		cfg.Database.User = user
	}
	if pass := os.Getenv("DB_PASSWORD"); pass != "" {
		cfg.Database.Password = pass
	}
	if dbname := os.Getenv("DB_NAME"); dbname != "" {
		cfg.Database.DBName = dbname
	}

	if maxQty := os.Getenv("MAX_QTY_PER_BATCH"); maxQty != "" {
		if q, err := strconv.Atoi(maxQty); err == nil && q > 0 {
			cfg.Worker.MaxQtyPerBatch = q
		}
	}

	if concurrency := os.Getenv("WORKER_CONCURRENCY"); concurrency != "" {
		if c, err := strconv.Atoi(concurrency); err == nil && c > 0 {
			cfg.Worker.Concurrency = c
		}
	}

	if pollInterval := os.Getenv("POLL_INTERVAL_SEC"); pollInterval != "" {
		if p, err := strconv.Atoi(pollInterval); err == nil && p > 0 {
			cfg.Worker.PollIntervalSec = p
		}
	}

	if port := os.Getenv("WEBHOOK_PORT"); port != "" {
		cfg.WebhookPort = port
	}

	return cfg, nil
}

func parseAllowedIDs(raw string) []int64 {
	var ids []int64
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// IsUserAllowed checks if a Telegram user ID is whitelisted.
func (c *Config) IsUserAllowed(userID int64) bool {
	if len(c.Telegram.AllowedUserIDs) == 0 {
		return false
	}
	for _, id := range c.Telegram.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}
