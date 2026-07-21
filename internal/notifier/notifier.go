package notifier

import (
	"context"
	"fmt"
	"html"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

// sender — то немногое из bot.Bot, что нужно нотифаеру. Введён ради теста:
// его реализует и настоящий бот, и заглушка, так что сборку сообщения можно
// проверить без похода в Telegram API.
type sender interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
}

type Telegram struct {
	bot    sender
	chatID int64
	log    *slog.Logger
}

// New поднимает бота и проверяет токен через getMe под нашим контекстом. getMe делаем
// сами (WithSkipGetMe гасит встроенный вызов), чтобы проверка отменялась общим ctx
// процесса, а не жила на собственном таймауте библиотеки. Проверка на старте — чтобы
// узнать про битый токен сразу, а не при первом изменении цены через сутки.
func New(ctx context.Context, token string, chatID int64, log *slog.Logger) (*Telegram, error) {
	b, err := bot.New(token, bot.WithSkipGetMe())
	if err != nil {
		return nil, fmt.Errorf("инициализация Telegram-бота: %w", err)
	}
	if _, err := b.GetMe(ctx); err != nil {
		return nil, fmt.Errorf("проверка токена Telegram (getMe): %w", err)
	}
	return &Telegram{bot: b, chatID: chatID, log: log}, nil
}

func (t *Telegram) NotifyChange(ctx context.Context, p domain.Product, old, current domain.Quote) error {
	_, err := t.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:             t.chatID,
		Text:               formatChange(p, old, current),
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()},
	})
	if err != nil {
		return fmt.Errorf("отправка уведомления в чат %d: %w", t.chatID, err)
	}
	return nil
}

// formatChange собирает текст уведомления. Вынесен из отправки и покрыт тестом:
// текст — это то, что видит пользователь, и ломать его молча нельзя.
func formatChange(p domain.Product, old, current domain.Quote) string {
	arrow, word := "🔺", "выросла"
	if current.Price < old.Price {
		arrow, word = "🔻", "упала"
	}

	// Имя товара приходит из БД и попадает в HTML-разметку — экранируем,
	// иначе «<» в названии сломает сообщение или разметку.
	name := html.EscapeString(p.Name)

	return fmt.Sprintf(
		"%s Цена %s\n\n<b>%s</b>\nбыло: %s\nстало: <b>%s</b>\n\n%s",
		arrow, word,
		name,
		old.Price.Format(old.Currency),
		current.Price.Format(current.Currency),
		p.URL,
	)
}
