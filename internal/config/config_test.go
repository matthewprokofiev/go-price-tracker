package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseCheckInterval(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "пусто — дефолт", raw: "", want: defaultCheckInterval},
		{name: "только пробелы — дефолт", raw: "   ", want: defaultCheckInterval},
		{name: "минуты", raw: "15m", want: 15 * time.Minute},
		{name: "час", raw: "1h", want: time.Hour},
		{name: "ровно минимум", raw: "30s", want: minCheckInterval},
		{name: "с пробелами по краям", raw: " 5m ", want: 5 * time.Minute},
		{name: "чаще минимума", raw: "1s", wantErr: true},
		{name: "ноль", raw: "0s", wantErr: true},
		{name: "отрицательный", raw: "-5m", wantErr: true},
		{name: "без единицы измерения", raw: "15", wantErr: true},
		{name: "мусор", raw: "скоро", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCheckInterval(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseCheckInterval(%q) = %v, ожидалась ошибка", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCheckInterval(%q): неожиданная ошибка: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("ParseCheckInterval(%q) = %v, ожидалось %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseChatID(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{name: "личный чат", raw: "123456789", want: 123456789},
		{name: "группа — id отрицательный", raw: "-1001234567890", want: -1001234567890},
		{name: "с пробелами по краям", raw: " 42 ", want: 42},
		{name: "пусто", raw: "", wantErr: true},
		{name: "ноль", raw: "0", wantErr: true},
		{name: "не число", raw: "@username", wantErr: true},
		{name: "дробное", raw: "12.5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseChatID(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseChatID(%q) = %v, ожидалась ошибка", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseChatID(%q): неожиданная ошибка: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("parseChatID(%q) = %d, ожидалось %d", tt.raw, got, tt.want)
			}
		})
	}
}

// Load обязан показать все проблемы разом, а не по одной за запуск.
func TestLoadReportsAllProblems(t *testing.T) {
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("NOTIFY_CHAT_ID", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CHECK_INTERVAL", "1s")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() без обязательных переменных обязан вернуть ошибку")
	}

	for _, want := range []string{"BOT_TOKEN", "NOTIFY_CHAT_ID", "DATABASE_URL", "CHECK_INTERVAL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет упоминания %s:\n%v", want, err)
		}
	}
}

func TestLoadValid(t *testing.T) {
	t.Setenv("BOT_TOKEN", "123:placeholder")
	t.Setenv("NOTIFY_CHAT_ID", "42")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("CHECK_INTERVAL", "5m")
	t.Setenv("APP_ENV", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): неожиданная ошибка: %v", err)
	}
	if cfg.NotifyChatID != 42 {
		t.Errorf("NotifyChatID = %d, ожидалось 42", cfg.NotifyChatID)
	}
	if cfg.CheckInterval != 5*time.Minute {
		t.Errorf("CheckInterval = %v, ожидалось 5m", cfg.CheckInterval)
	}
	if cfg.AppEnv != EnvLocal {
		t.Errorf("AppEnv = %q, ожидался дефолт %q", cfg.AppEnv, EnvLocal)
	}
}
