package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvLocal = "local"

	defaultCheckInterval = 15 * time.Minute
	minCheckInterval     = 30 * time.Second
)

type Config struct {
	BotToken      string
	NotifyChatID  int64
	DatabaseURL   string
	CheckInterval time.Duration
	AppEnv        string
}

// Load собирает конфиг из ENV и падает при отсутствии критичных переменных:
// парсер без БД и без канала уведомлений всё равно нежизнеспособен, лучше узнать это на старте.
// Все проблемы копятся в срез и возвращаются одной ошибкой — иначе запуск превращается
// в игру «почини переменную — узнай про следующую».
func Load() (Config, error) {
	var cfg Config
	var problems []string

	cfg.BotToken = os.Getenv("BOT_TOKEN")
	if cfg.BotToken == "" {
		problems = append(problems, "BOT_TOKEN не задан: получите токен у @BotFather")
	}

	chatID, err := parseChatID(os.Getenv("NOTIFY_CHAT_ID"))
	if err != nil {
		problems = append(problems, fmt.Sprintf("NOTIFY_CHAT_ID: %v", err))
	}
	cfg.NotifyChatID = chatID

	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL не задан: например postgres://user:pass@host:5432/db?sslmode=disable")
	}

	cfg.CheckInterval, err = ParseCheckInterval(os.Getenv("CHECK_INTERVAL"))
	if err != nil {
		problems = append(problems, err.Error())
	}

	cfg.AppEnv = envOrDefault("APP_ENV", EnvLocal)

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("некорректная конфигурация:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}

// ParseCheckInterval разбирает период обхода и держит нижнюю границу: слишком частый
// опрос чужого сайта — это уже не мониторинг, а нагрузка на чужой сервер.
func ParseCheckInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultCheckInterval, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return defaultCheckInterval, fmt.Errorf("CHECK_INTERVAL=%q: ожидается длительность вида 30s, 15m, 1h", raw)
	}
	if d < minCheckInterval {
		return defaultCheckInterval, fmt.Errorf("CHECK_INTERVAL=%s: минимум %s — чаще опрашивать чужой сайт невежливо", d, minCheckInterval)
	}
	return d, nil
}

// parseChatID: id группы у Telegram отрицательный, поэтому проверяется только "не ноль".
func parseChatID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("не задан: узнать свой id можно у @userinfobot")
	}

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q не является числовым chat_id", raw)
	}
	if id == 0 {
		return 0, fmt.Errorf("chat_id не может быть нулём")
	}
	return id, nil
}

func NewLogger(appEnv string) *slog.Logger {
	if appEnv == EnvLocal {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
