package scraper

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

// DefaultUserAgent честно представляется и оставляет ссылку, по которой админ сайта
// поймёт, кто к нему ходит, и найдёт, кому писать. Маскироваться под Chrome — обычная
// практика парсеров, но это ровно тот случай, когда «работает» и «правильно» расходятся:
// подделка UA лишает админа возможности отличить бота от людей и заблокировать только его.
const DefaultUserAgent = "go-price-tracker/1.0 (+https://github.com/matveiprokofev/go-price-tracker)"

// ErrRobotsDisallow — robots.txt запрещает нам эту страницу. Это не сбой: это ответ
// сайта «сюда не ходи», и ретраить его бессмысленно.
var ErrRobotsDisallow = fmt.Errorf("robots.txt запрещает скачивание")

type Options struct {
	UserAgent string
	Timeout   time.Duration

	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration

	// MaxRetryWait — потолок паузы, которую мы готовы выждать по Retry-After внутри
	// одного прогона. Просит сайт ждать дольше — товар пропускается до следующего тика,
	// а хост придерживается, чтобы туда никто не стучался. Это отдельный лимит от
	// MaxBackoff: backoff — наша пауза, MaxRetryWait — согласие ждать по чужой просьбе.
	MaxRetryWait time.Duration

	// HostConcurrency и HostDelay — вежливость на хост, независимая от общей
	// конкурентности обхода. См. hostLimiter.
	HostConcurrency int
	HostDelay       time.Duration
}

func DefaultOptions() Options {
	return Options{
		UserAgent: DefaultUserAgent,
		// Таймаут на весь запрос вместе с чтением тела: без него зависший сервер
		// держал бы слот хоста до бесконечности и прогон никогда бы не кончился.
		Timeout:      15 * time.Second,
		MaxAttempts:  3,
		BaseBackoff:  time.Second,
		MaxBackoff:   30 * time.Second,
		MaxRetryWait: 2 * time.Minute,
		// Два соединения и секунда между запросами — нагрузка, которую не заметит
		// даже скромный сайт. Мониторинг цен никуда не спешит.
		HostConcurrency: 2,
		HostDelay:       time.Second,
	}
}

type Fetcher struct {
	client *http.Client
	log    *slog.Logger
	opts   Options
	hosts  *hostLimiter
	robots *robotsCache
}

func New(opts Options, log *slog.Logger) *Fetcher {
	client := &http.Client{Timeout: opts.Timeout}

	return &Fetcher{
		client: client,
		log:    log,
		opts:   opts,
		hosts:  newHostLimiter(opts.HostConcurrency, opts.HostDelay),
		robots: newRobotsCache(client, opts.UserAgent),
	}
}

// Quote скачивает страницу товара и достаёт с неё цену.
func (f *Fetcher) Quote(ctx context.Context, p domain.Product) (domain.Quote, error) {
	target, err := url.Parse(p.URL)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("некорректный URL %q: %w", p.URL, err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return domain.Quote{}, fmt.Errorf("неподдерживаемая схема %q в URL %q", target.Scheme, p.URL)
	}

	allowed, err := f.robots.allowed(ctx, target)
	if err != nil {
		return domain.Quote{}, err
	}
	if !allowed {
		return domain.Quote{}, fmt.Errorf("%w: %s", ErrRobotsDisallow, p.URL)
	}

	// Сайт вправе попросить ходить реже, чем наш дефолт. Просьбу выполняем.
	if delay := f.robots.crawlDelay(target); delay > f.opts.HostDelay {
		f.hosts.penalize(target.Host, delay)
	}

	body, err := f.fetch(ctx, p.URL, target.Host)
	if err != nil {
		return domain.Quote{}, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return domain.Quote{}, fmt.Errorf("разбор HTML %s: %w", p.URL, err)
	}
	return ExtractPrice(doc, p.CSSSelector)
}
