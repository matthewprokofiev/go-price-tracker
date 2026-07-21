package monitor

import (
	"context"
	"log/slog"
	"runtime/debug"

	"golang.org/x/sync/errgroup"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

// Интерфейсы узкие и объявлены здесь, у потребителя: monitor не тащит зависимость
// на pgx, http и telegram и тестируется на подделках без сети и БД.

type PriceSource interface {
	Quote(ctx context.Context, p domain.Product) (domain.Quote, error)
}

type Store interface {
	ActiveProducts(ctx context.Context) ([]domain.Product, error)
	LastQuote(ctx context.Context, productID int64) (*domain.Quote, error)
	AddPrice(ctx context.Context, productID int64, q domain.Quote, notified bool) (int64, error)
	MarkNotified(ctx context.Context, priceID int64) error
	PendingNotifications(ctx context.Context) ([]domain.PendingNotification, error)
}

type Notifier interface {
	NotifyChange(ctx context.Context, p domain.Product, old, current domain.Quote) error
}

type Monitor struct {
	source   PriceSource
	store    Store
	notifier Notifier
	log      *slog.Logger

	// concurrency — верхняя граница числа одновременно обрабатываемых товаров.
	// Реальную нагрузку на конкретный сайт держит rate limit на хост внутри source;
	// здесь ограничивается ширина всего прогона (память, число открытых соединений).
	concurrency int
}

func New(source PriceSource, store Store, notifier Notifier, concurrency int, log *slog.Logger) *Monitor {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Monitor{
		source:      source,
		store:       store,
		notifier:    notifier,
		log:         log,
		concurrency: concurrency,
	}
}

// RunOnce обрабатывает все активные товары один раз.
//
// errgroup здесь — семафор (SetLimit), а не механизм отмены. Из воркера всегда
// возвращается nil: верни он ошибку — errgroup отменил бы контекст и оборвал обработку
// остальных товаров. Один битый товар (упавший селектор, 404, недоступный хост)
// логируется per-product и не роняет прогон.
//
// Ошибку возвращает только загрузка списка товаров: без списка прогону нечего делать.
func (m *Monitor) RunOnce(ctx context.Context) error {
	// Сначала добираем уведомления, не дошедшие в прошлые прогоны: делаем это до обхода,
	// последовательно, пока не начались конкурентные воркеры — так нет гонки за MarkNotified.
	m.retryPending(ctx)

	products, err := m.store.ActiveProducts(ctx)
	if err != nil {
		return err
	}
	if len(products) == 0 {
		m.log.Info("активных товаров нет, прогон пропущен")
		return nil
	}

	m.log.Info("старт прогона", "products", len(products), "concurrency", m.concurrency)

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(m.concurrency)

	for _, p := range products {
		g.Go(func() error {
			// Паника одного товара не должна ронять процесс: errgroup не восстанавливает
			// паники, поэтому recover здесь — иначе битый HTML или баг в библиотеке
			// убил бы весь демон вместе с остальными товарами.
			defer func() {
				if r := recover(); r != nil {
					m.log.Error("паника при обработке товара",
						"product_id", p.ID, "product", p.Name, "panic", r,
						"stack", string(debug.Stack()))
				}
			}()

			// Контекст проверяем сами: при отмене (shutdown) выходим тихо, но nil,
			// чтобы не гасить остальных через errgroup — это их доделает следующий тик.
			if ctx.Err() != nil {
				return nil
			}
			m.processOne(ctx, p)
			return nil
		})
	}

	// Ошибки быть не может — воркеры всегда возвращают nil, — но результат проверяем
	// явно, иначе линтер (и следующий читатель) справедливо усомнится.
	if err := g.Wait(); err != nil {
		return err
	}
	m.log.Info("прогон завершён")
	return nil
}

// retryPending досылает уведомления об изменениях, о которых не удалось сообщить раньше
// (Telegram лежал, сеть моргнула). Без этого запись цены «съедала» изменение: следующий
// прогон видел бы цену неизменной и уже не уведомил.
func (m *Monitor) retryPending(ctx context.Context) {
	pending, err := m.store.PendingNotifications(ctx)
	if err != nil {
		m.log.Error("не удалось прочитать недоставленные уведомления", "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	m.log.Info("добор недоставленных уведомлений", "count", len(pending))

	for _, n := range pending {
		if ctx.Err() != nil {
			return
		}
		if err := m.notifier.NotifyChange(ctx, n.Product, n.Old, n.New); err != nil {
			m.log.Error("добор уведомления не удался", "price_id", n.PriceID, "product", n.Product.Name, "error", err)
			continue
		}
		if err := m.store.MarkNotified(ctx, n.PriceID); err != nil {
			m.log.Error("не удалось отметить уведомление доставленным", "price_id", n.PriceID, "error", err)
		}
	}
}

func (m *Monitor) processOne(ctx context.Context, p domain.Product) {
	log := m.log.With("product_id", p.ID, "product", p.Name)

	current, err := m.source.Quote(ctx, p)
	if err != nil {
		log.Error("не удалось получить цену", "error", err)
		return
	}

	previous, err := m.store.LastQuote(ctx, p.ID)
	if err != nil {
		log.Error("не удалось прочитать последнюю цену", "error", err)
		return
	}

	kind := domain.Classify(previous, current)
	if !kind.ShouldRecord() {
		log.Debug("цена не изменилась", "price", current.Price.Format(current.Currency))
		return
	}

	// notified фиксируется вместе с ценой: первое наблюдение уведомления не требует
	// (notified=true), изменение — требует (notified=false до успешной отправки).
	// Если отправка провалится, строка останется notified=false и её добьёт retryPending.
	notified := !kind.ShouldNotify()

	priceID, err := m.store.AddPrice(ctx, p.ID, current, notified)
	if err != nil {
		log.Error("не удалось записать цену", "error", err)
		return
	}
	log.Info("цена записана", "kind", kind.String(), "price", current.Price.Format(current.Currency))

	if !kind.ShouldNotify() {
		return
	}

	// Уведомление шлётся только после успешной записи: сообщать об изменении, которое
	// не сохранилось, нельзя. Сбой отправки не теряется — строка остаётся notified=false,
	// и следующий прогон досылает её через retryPending.
	if err := m.notifier.NotifyChange(ctx, p, *previous, current); err != nil {
		log.Error("не удалось отправить уведомление, будет добор", "error", err)
		return
	}
	if err := m.store.MarkNotified(ctx, priceID); err != nil {
		log.Error("уведомление отправлено, но не удалось отметить доставленным", "price_id", priceID, "error", err)
	}
}
