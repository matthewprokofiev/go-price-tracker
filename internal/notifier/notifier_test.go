package notifier

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

func rub(minor int64) domain.Quote { return domain.Quote{Price: domain.Money(minor), Currency: "RUB"} }

func TestFormatChange(t *testing.T) {
	p := domain.Product{Name: "Кофеварка Bravo X200", URL: "https://shop.test/bravo"}

	t.Run("цена упала", func(t *testing.T) {
		got := formatChange(p, rub(100000), rub(90000))
		for _, want := range []string{"упала", "🔻", "было: 1 000 ₽", "стало: <b>900 ₽</b>", "https://shop.test/bravo"} {
			if !strings.Contains(got, want) {
				t.Errorf("в сообщении нет %q:\n%s", want, got)
			}
		}
	})

	t.Run("цена выросла", func(t *testing.T) {
		got := formatChange(p, rub(90000), rub(100000))
		for _, want := range []string{"выросла", "🔺", "было: 900 ₽", "стало: <b>1 000 ₽</b>"} {
			if !strings.Contains(got, want) {
				t.Errorf("в сообщении нет %q:\n%s", want, got)
			}
		}
	})
}

// Имя товара из БД не должно ломать HTML-разметку сообщения.
func TestFormatChangeEscapesHTML(t *testing.T) {
	p := domain.Product{Name: `Товар <b> & "спецы"`, URL: "https://shop.test/x"}

	got := formatChange(p, rub(100000), rub(90000))
	if strings.Contains(got, "<b>Товар <b>") {
		t.Errorf("имя товара не экранировано:\n%s", got)
	}
	if !strings.Contains(got, "&lt;b&gt;") {
		t.Errorf("ожидалось экранирование '<b>' в имени:\n%s", got)
	}
}

type spySender struct {
	params *bot.SendMessageParams
}

func (s *spySender) SendMessage(_ context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	s.params = params
	return &models.Message{}, nil
}

func TestNotifyChangeSendsToConfiguredChat(t *testing.T) {
	spy := &spySender{}
	tg := &Telegram{bot: spy, chatID: 42, log: nil}

	p := domain.Product{Name: "Товар", URL: "https://shop.test/x"}
	if err := tg.NotifyChange(context.Background(), p, rub(100000), rub(90000)); err != nil {
		t.Fatalf("NotifyChange(): %v", err)
	}

	if spy.params == nil {
		t.Fatal("SendMessage не вызван")
	}
	if spy.params.ChatID != int64(42) {
		t.Errorf("ChatID = %v, ожидалось 42", spy.params.ChatID)
	}
	if spy.params.ParseMode != models.ParseModeHTML {
		t.Errorf("ParseMode = %q, ожидался HTML", spy.params.ParseMode)
	}
}
