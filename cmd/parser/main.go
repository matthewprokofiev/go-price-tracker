package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/matveiprokofev/go-price-tracker/internal/config"
	"github.com/matveiprokofev/go-price-tracker/internal/exporter"
	"github.com/matveiprokofev/go-price-tracker/internal/monitor"
	"github.com/matveiprokofev/go-price-tracker/internal/notifier"
	"github.com/matveiprokofev/go-price-tracker/internal/scraper"
	"github.com/matveiprokofev/go-price-tracker/internal/storage"
)

// crawlConcurrency — сколько товаров обрабатывается одновременно. Реальную нагрузку
// на конкретный сайт держит rate limit на хост внутри scraper, здесь — ширина прогона.
const crawlConcurrency = 5

func main() {
	if err := run(); err != nil {
		// Логгер к этому моменту мог быть ещё не создан (ошибка конфига), поэтому в stderr.
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run() error {
	exportPath := parseFlags()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := config.NewLogger(cfg.AppEnv)

	// Один ctx на весь процесс: он же уходит в миграции, сид, обход и long-lived цикл.
	// По SIGINT/SIGTERM отменяется — и всё дерево операций сворачивается разом.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := storage.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("миграции: %w", err)
	}

	store, err := storage.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer store.Close()

	// Режим разовой выгрузки: экспортируем историю и выходим, бота не поднимаем.
	if exportPath != "" {
		return runExport(ctx, store, exportPath, log)
	}

	if err := store.Seed(ctx); err != nil {
		return fmt.Errorf("сид товаров: %w", err)
	}

	notify, err := notifier.New(ctx, cfg.BotToken, cfg.NotifyChatID, log)
	if err != nil {
		return err
	}

	fetcher := scraper.New(scraper.DefaultOptions(), log)
	mon := monitor.New(fetcher, store, notify, crawlConcurrency, log)

	log.Info("парсер запущен", "interval", cfg.CheckInterval.String(), "app_env", cfg.AppEnv)
	return mon.Run(ctx, cfg.CheckInterval)
}

func parseFlags() string {
	var exportPath string
	flag.StringVar(&exportPath, "export", "", "выгрузить историю цен в указанный .xlsx и выйти")
	flag.Parse()
	return exportPath
}

func runExport(ctx context.Context, store *storage.Storage, path string, log *slog.Logger) error {
	rows, err := store.History(ctx)
	if err != nil {
		return fmt.Errorf("чтение истории: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("создание файла %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if err := exporter.WriteXLSX(f, rows); err != nil {
		return err
	}
	log.Info("история выгружена", "file", path, "rows", len(rows))
	return nil
}
