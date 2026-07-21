package scraper

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

func testOptions() Options {
	o := DefaultOptions()
	// Тесты не должны реально спать секундами: ужимаем все паузы.
	o.BaseBackoff = time.Millisecond
	o.MaxBackoff = 20 * time.Millisecond
	o.HostDelay = time.Millisecond
	o.Timeout = 2 * time.Second
	// Retry-After в тестах измеряется секундами — согласие ждать держим выше.
	o.MaxRetryWait = 5 * time.Second
	return o
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// robots.txt отдаём разрешающим, чтобы тесты fetch проверяли именно логику загрузки.
func withRobots(mux *http.ServeMux) *http.ServeMux {
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "User-agent: *\nAllow: /\n")
	})
	return mux
}

const priceHTML = `<html><body><span class="price">1 000 ₽</span></body></html>`

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		header string
		want   time.Duration
		wantOK bool
	}{
		{name: "секунды", header: "120", want: 120 * time.Second, wantOK: true},
		{name: "ноль секунд", header: "0", want: 0, wantOK: true},
		{name: "дата в будущем", header: now.Add(30 * time.Second).Format(http.TimeFormat), want: 30 * time.Second, wantOK: true},
		{name: "дата в прошлом — уже можно", header: now.Add(-time.Minute).Format(http.TimeFormat), want: 0, wantOK: true},
		{name: "пусто", header: "", wantOK: false},
		{name: "мусор", header: "скоро", wantOK: false},
		{name: "отрицательные секунды", header: "-5", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRetryAfter(tt.header, now)
			if ok != tt.wantOK {
				t.Fatalf("parseRetryAfter(%q) ok = %v, ожидалось %v", tt.header, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, ожидалось %v", tt.header, got, tt.want)
			}
		})
	}
}

// 429 с Retry-After: парсер обязан выждать указанное время и повторить, а не сдаться.
func TestFetch429WithRetryAfter(t *testing.T) {
	var hits int32

	mux := withRobots(http.NewServeMux())
	mux.HandleFunc("/product", func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, priceHTML)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(testOptions(), discardLogger())

	start := time.Now()
	quote, err := f.Quote(context.Background(), product(srv.URL+"/product", ".price"))
	if err != nil {
		t.Fatalf("Quote(): %v", err)
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("Retry-After=1s не соблюдён: прошло %v", elapsed)
	}
	if quote.Price != domain.Money(100000) {
		t.Errorf("Price = %d, ожидалось 100000", quote.Price)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("серверу пришло %d запросов, ожидалось 2", got)
	}
}

// 429 без Retry-After: повтор всё равно должен случиться, паузу назначаем сами (backoff).
func TestFetch429WithoutRetryAfter(t *testing.T) {
	var hits int32

	mux := withRobots(http.NewServeMux())
	mux.HandleFunc("/product", func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, priceHTML)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(testOptions(), discardLogger())

	quote, err := f.Quote(context.Background(), product(srv.URL+"/product", ".price"))
	if err != nil {
		t.Fatalf("Quote(): %v", err)
	}
	if quote.Price != domain.Money(100000) {
		t.Errorf("Price = %d, ожидалось 100000", quote.Price)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("серверу пришло %d запросов, ожидалось 2", got)
	}
}

// Постоянный 429 не должен молотиться бесконечно: попытки исчерпываются и возвращается ошибка.
func TestFetch429Persistent(t *testing.T) {
	var hits int32

	mux := withRobots(http.NewServeMux())
	mux.HandleFunc("/product", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(testOptions(), discardLogger())

	_, err := f.Quote(context.Background(), product(srv.URL+"/product", ".price"))
	if !errors.Is(err, ErrTooManyRequests) {
		t.Fatalf("ожидалась ErrTooManyRequests, получено: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != int32(testOptions().MaxAttempts) {
		t.Errorf("серверу пришло %d запросов, ожидалось %d", got, testOptions().MaxAttempts)
	}
}

// 5xx — временная ошибка: повторяем и добираемся до успеха.
func TestFetchRetriesOn5xx(t *testing.T) {
	var hits int32

	mux := withRobots(http.NewServeMux())
	mux.HandleFunc("/product", func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, priceHTML)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(testOptions(), discardLogger())

	if _, err := f.Quote(context.Background(), product(srv.URL+"/product", ".price")); err != nil {
		t.Fatalf("Quote(): %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("серверу пришло %d запросов, ожидалось 3", got)
	}
}

// 404 повторять бессмысленно: одна попытка и ошибка.
func TestFetchNoRetryOn404(t *testing.T) {
	var hits int32

	mux := withRobots(http.NewServeMux())
	mux.HandleFunc("/product", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(testOptions(), discardLogger())

	if _, err := f.Quote(context.Background(), product(srv.URL+"/product", ".price")); err == nil {
		t.Fatal("ожидалась ошибка на 404")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("серверу пришло %d запросов, ожидалось 1 (404 не повторяют)", got)
	}
}

// robots.txt запрещает путь — до самой страницы дело не доходит.
func TestFetchRespectsRobotsDisallow(t *testing.T) {
	var productHits int32

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "User-agent: *\nDisallow: /secret/\n")
	})
	mux.HandleFunc("/secret/product", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&productHits, 1)
		_, _ = io.WriteString(w, priceHTML)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(testOptions(), discardLogger())

	_, err := f.Quote(context.Background(), product(srv.URL+"/secret/product", ".price"))
	if !errors.Is(err, ErrRobotsDisallow) {
		t.Fatalf("ожидалась ErrRobotsDisallow, получено: %v", err)
	}
	if got := atomic.LoadInt32(&productHits); got != 0 {
		t.Errorf("запрещённая страница скачана %d раз, ожидалось 0", got)
	}
}

func product(url, selector string) domain.Product {
	return domain.Product{ID: 1, Name: "Тест", URL: url, CSSSelector: selector, IsActive: true}
}
