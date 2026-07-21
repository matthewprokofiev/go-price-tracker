package scraper

import (
	"context"
	"sync"
	"time"
)

// hostLimiter делает вежливость свойством хоста, а не воркера.
//
// Ограничение конкурентности из errgroup (SetLimit) считает воркеров, но ничего
// не знает про то, куда они ходят: пять воркеров с товарами одного магазина — это
// пять одновременных запросов к одному серверу. Поэтому лимит на хост отдельный:
// не больше concurrency соединений и не чаще, чем раз в delay, к каждому хосту.
type hostLimiter struct {
	mu    sync.Mutex
	hosts map[string]*hostState

	concurrency int
	delay       time.Duration
}

type hostState struct {
	sem chan struct{}

	mu          sync.Mutex
	nextAllowed time.Time
}

func newHostLimiter(concurrency int, delay time.Duration) *hostLimiter {
	if concurrency < 1 {
		concurrency = 1
	}
	return &hostLimiter{
		hosts:       make(map[string]*hostState),
		concurrency: concurrency,
		delay:       delay,
	}
}

func (l *hostLimiter) state(host string) *hostState {
	l.mu.Lock()
	defer l.mu.Unlock()

	s, ok := l.hosts[host]
	if !ok {
		s = &hostState{sem: make(chan struct{}, l.concurrency)}
		l.hosts[host] = s
	}
	return s
}

// acquire занимает слот хоста и выдерживает паузу до момента, когда к этому хосту
// снова можно обращаться. Возвращает функцию освобождения слота.
func (l *hostLimiter) acquire(ctx context.Context, host string) (func(), error) {
	s := l.state(host)

	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-s.sem }

	// Слот во времени резервируется до сна, а не после: иначе два воркера,
	// одновременно увидев «ждать не нужно», ушли бы в запрос в одну и ту же миллисекунду.
	s.mu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if s.nextAllowed.After(now) {
		wait = s.nextAllowed.Sub(now)
	}
	s.nextAllowed = now.Add(wait).Add(l.delay)
	s.mu.Unlock()

	if err := sleepCtx(ctx, wait); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// penalize отодвигает право обращаться к хосту для всех воркеров сразу.
// Нужен для 429: back off должен касаться сайта целиком, иначе остальные воркеры
// продолжат долбить сервер, который только что попросил притормозить.
func (l *hostLimiter) penalize(host string, d time.Duration) {
	s := l.state(host)

	s.mu.Lock()
	defer s.mu.Unlock()

	if until := time.Now().Add(d); until.After(s.nextAllowed) {
		s.nextAllowed = until
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
