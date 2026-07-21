package monitor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

// Первый прогон обязан случиться сразу, до первого тика: даже если отменить контекст
// раньше, чем пройдёт интервал, один прогон уже должен состояться.
func TestRunExecutesImmediatelyBeforeTicker(t *testing.T) {
	src := &fakeSource{quotes: map[int64]domain.Quote{1: rub(100000)}}
	store := &fakeStore{products: products(1), last: map[int64]domain.Quote{}}
	m := New(src, store, &fakeNotifier{}, 2, quiet())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, time.Hour) }() // интервал заведомо больше теста

	// Ждём, пока первый прогон отработает, и гасим — до тикера дело не дойдёт.
	waitFor(t, func() bool { return atomic.LoadInt32(&src.calls) >= 1 })
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() вернул ошибку: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() не завершился после отмены контекста")
	}

	if got := atomic.LoadInt32(&src.calls); got != 1 {
		t.Errorf("до тикера ожидался ровно 1 прогон, было %d", got)
	}
}

// Тикер запускает последующие прогоны, а ctx.Done() завершает цикл без утечки.
func TestRunTicksThenStops(t *testing.T) {
	src := &fakeSource{quotes: map[int64]domain.Quote{1: rub(100000)}}
	store := &fakeStore{products: products(1), last: map[int64]domain.Quote{}}
	m := New(src, store, &fakeNotifier{}, 2, quiet())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, 20*time.Millisecond) }()

	// Первый прогон плюс хотя бы пара по тикеру.
	waitFor(t, func() bool { return atomic.LoadInt32(&src.calls) >= 3 })
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run() не завершился после отмены контекста")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("условие не наступило за отведённое время")
}
