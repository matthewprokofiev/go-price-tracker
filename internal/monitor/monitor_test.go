package monitor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeSource struct {
	quotes  map[int64]domain.Quote
	errs    map[int64]error
	panicOn map[int64]bool
	calls   int32
}

func (f *fakeSource) Quote(_ context.Context, p domain.Product) (domain.Quote, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.panicOn[p.ID] {
		panic("паника в обработке товара")
	}
	if err := f.errs[p.ID]; err != nil {
		return domain.Quote{}, err
	}
	return f.quotes[p.ID], nil
}

type priceRow struct {
	id       int64
	product  int64
	q        domain.Quote
	notified bool
}

// fakeStore ведёт себя как БД: добавленная цена становится «последней», а недоставленные
// строки собираются с восстановлением предыдущей цены — как реальный оконный запрос.
type fakeStore struct {
	mu       sync.Mutex
	products []domain.Product
	last     map[int64]domain.Quote // цены, «уже лежавшие» до прогона (сид истории)
	added    map[int64][]domain.Quote
	rows     []*priceRow
	nextID   int64
}

func (s *fakeStore) ActiveProducts(context.Context) ([]domain.Product, error) {
	return s.products, nil
}

func (s *fakeStore) LastQuote(_ context.Context, id int64) (*domain.Quote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.rows) - 1; i >= 0; i-- {
		if s.rows[i].product == id {
			q := s.rows[i].q
			return &q, nil
		}
	}
	if q, ok := s.last[id]; ok {
		return &q, nil
	}
	return nil, nil
}

func (s *fakeStore) AddPrice(_ context.Context, id int64, q domain.Quote, notified bool) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.added == nil {
		s.added = make(map[int64][]domain.Quote)
	}
	s.added[id] = append(s.added[id], q)
	s.nextID++
	s.rows = append(s.rows, &priceRow{id: s.nextID, product: id, q: q, notified: notified})
	return s.nextID, nil
}

func (s *fakeStore) MarkNotified(_ context.Context, priceID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.id == priceID {
			r.notified = true
			return nil
		}
	}
	return nil
}

func (s *fakeStore) PendingNotifications(context.Context) ([]domain.PendingNotification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []domain.PendingNotification
	for i, r := range s.rows {
		if r.notified {
			continue
		}
		var prev *domain.Quote
		for j := i - 1; j >= 0; j-- {
			if s.rows[j].product == r.product {
				q := s.rows[j].q
				prev = &q
				break
			}
		}
		if prev == nil {
			if q, ok := s.last[r.product]; ok {
				prev = &q
			}
		}
		if prev == nil {
			continue // первое наблюдение: предыдущей цены нет, уведомлять не о чем
		}
		out = append(out, domain.PendingNotification{
			PriceID: r.id,
			Product: s.productByID(r.product),
			Old:     *prev,
			New:     r.q,
		})
	}
	return out, nil
}

func (s *fakeStore) productByID(id int64) domain.Product {
	for _, p := range s.products {
		if p.ID == id {
			return p
		}
	}
	return domain.Product{ID: id}
}

type fakeNotifier struct {
	mu        sync.Mutex
	failTimes int // сколько первых вызовов упадёт
	attempts  int
	delivered int
}

func (n *fakeNotifier) NotifyChange(context.Context, domain.Product, domain.Quote, domain.Quote) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.attempts++
	if n.attempts <= n.failTimes {
		return errors.New("telegram недоступен")
	}
	n.delivered++
	return nil
}

func rub(minor int64) domain.Quote { return domain.Quote{Price: domain.Money(minor), Currency: "RUB"} }

func products(ids ...int64) []domain.Product {
	var out []domain.Product
	for _, id := range ids {
		out = append(out, domain.Product{ID: id, Name: "Товар", URL: "https://example.test", CSSSelector: ".p"})
	}
	return out
}

// Ядро требования: один битый товар (ошибка) не прерывает обработку остальных.
func TestRunOnceIsolatesFailures(t *testing.T) {
	src := &fakeSource{
		quotes: map[int64]domain.Quote{1: rub(100000), 3: rub(300000)},
		errs:   map[int64]error{2: errors.New("селектор не найден")},
	}
	store := &fakeStore{products: products(1, 2, 3), last: map[int64]domain.Quote{}}
	notifier := &fakeNotifier{}

	m := New(src, store, notifier, 5, quiet())
	if err := m.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(): %v", err)
	}

	if got := atomic.LoadInt32(&src.calls); got != 3 {
		t.Errorf("source вызван %d раз, ожидалось 3", got)
	}
	if _, ok := store.added[1]; !ok {
		t.Error("цена товара 1 не записана")
	}
	if _, ok := store.added[3]; !ok {
		t.Error("цена товара 3 не записана")
	}
	if _, ok := store.added[2]; ok {
		t.Error("у битого товара 2 не должно быть записи")
	}
}

// Паника при обработке товара не должна ронять прогон и остальные товары.
func TestRunOncePanicIsolated(t *testing.T) {
	src := &fakeSource{
		quotes:  map[int64]domain.Quote{1: rub(100000), 3: rub(300000)},
		panicOn: map[int64]bool{2: true},
	}
	store := &fakeStore{products: products(1, 2, 3), last: map[int64]domain.Quote{}}

	m := New(src, store, &fakeNotifier{}, 5, quiet())
	if err := m.RunOnce(context.Background()); err != nil {
		t.Fatalf("паника одного товара не должна возвращать ошибку прогона: %v", err)
	}

	if _, ok := store.added[1]; !ok {
		t.Error("товар 1 не обработан после паники товара 2")
	}
	if _, ok := store.added[3]; !ok {
		t.Error("товар 3 не обработан после паники товара 2")
	}
	if _, ok := store.added[2]; ok {
		t.Error("у паникнувшего товара 2 не должно быть записи")
	}
}

func TestRunOnceFirstSeenRecordsWithoutNotify(t *testing.T) {
	src := &fakeSource{quotes: map[int64]domain.Quote{1: rub(100000)}}
	store := &fakeStore{products: products(1), last: map[int64]domain.Quote{}}
	notifier := &fakeNotifier{}

	m := New(src, store, notifier, 2, quiet())
	if err := m.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(): %v", err)
	}

	if len(store.added[1]) != 1 {
		t.Errorf("ожидалась запись базовой цены, добавлено: %v", store.added[1])
	}
	if notifier.delivered != 0 {
		t.Errorf("на первом наблюдении уведомлений быть не должно, доставлено %d", notifier.delivered)
	}
}

func TestRunOnceChangedRecordsAndNotifies(t *testing.T) {
	src := &fakeSource{quotes: map[int64]domain.Quote{1: rub(90000)}}
	store := &fakeStore{products: products(1), last: map[int64]domain.Quote{1: rub(100000)}}
	notifier := &fakeNotifier{}

	m := New(src, store, notifier, 2, quiet())
	if err := m.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(): %v", err)
	}

	if len(store.added[1]) != 1 {
		t.Errorf("изменение цены должно записаться, добавлено: %v", store.added[1])
	}
	if notifier.delivered != 1 {
		t.Errorf("ожидалась 1 доставка, доставлено %d", notifier.delivered)
	}
}

func TestRunOnceUnchangedDoesNothing(t *testing.T) {
	src := &fakeSource{quotes: map[int64]domain.Quote{1: rub(100000)}}
	store := &fakeStore{products: products(1), last: map[int64]domain.Quote{1: rub(100000)}}
	notifier := &fakeNotifier{}

	m := New(src, store, notifier, 2, quiet())
	if err := m.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(): %v", err)
	}

	if len(store.added[1]) != 0 {
		t.Errorf("неизменную цену писать не нужно, добавлено: %v", store.added[1])
	}
	if notifier.delivered != 0 {
		t.Errorf("уведомлений быть не должно, доставлено %d", notifier.delivered)
	}
}

// Недоставленное уведомление (Telegram упал) не теряется: следующий прогон его добирает,
// даже если цена больше не менялась.
func TestRunOnceRetriesFailedNotification(t *testing.T) {
	src := &fakeSource{quotes: map[int64]domain.Quote{1: rub(90000)}}
	store := &fakeStore{products: products(1), last: map[int64]domain.Quote{1: rub(100000)}}
	notifier := &fakeNotifier{failTimes: 1}

	m := New(src, store, notifier, 2, quiet())

	// Прогон 1: изменение записано, но отправка упала — строка остаётся недоставленной.
	if err := m.RunOnce(context.Background()); err != nil {
		t.Fatalf("прогон 1: %v", err)
	}
	if notifier.delivered != 0 {
		t.Fatalf("после сбоя доставок быть не должно, доставлено %d", notifier.delivered)
	}
	if pending := mustPending(t, store); len(pending) != 1 {
		t.Fatalf("ожидалась 1 недоставленная строка, получено %d", len(pending))
	}

	// Прогон 2: цена та же (unchanged), новой записи нет, но добор дошлёт недоставленное.
	if err := m.RunOnce(context.Background()); err != nil {
		t.Fatalf("прогон 2: %v", err)
	}
	if notifier.delivered != 1 {
		t.Fatalf("добор не сработал, доставлено %d", notifier.delivered)
	}
	if len(store.added[1]) != 1 {
		t.Errorf("во втором прогоне не должно появиться новой записи, добавлено: %v", store.added[1])
	}
	if pending := mustPending(t, store); len(pending) != 0 {
		t.Fatalf("после добора недоставленных быть не должно, осталось %d", len(pending))
	}
}

func mustPending(t *testing.T, s *fakeStore) []domain.PendingNotification {
	t.Helper()
	p, err := s.PendingNotifications(context.Background())
	if err != nil {
		t.Fatalf("PendingNotifications(): %v", err)
	}
	return p
}
