package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// maxBodySize: страница товара — это сотни килобайт. Лимит защищает от того,
// чтобы «страница» на десяток гигабайт съела память процесса.
const maxBodySize = 8 << 20

// ErrTooManyRequests: сайт попросил притормозить, и пауза, которую он назвал,
// длиннее, чем разумно ждать внутри одного прогона.
var ErrTooManyRequests = errors.New("сайт ограничивает частоту запросов (429)")

// httpError — ответ, который не 200. Хранит код, чтобы вызывающий код мог
// отличить «страница удалена» (404) от «сервер прилёг» (503).
type httpError struct {
	StatusCode int
	URL        string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("%s вернул %d %s", e.URL, e.StatusCode, http.StatusText(e.StatusCode))
}

// fetch скачивает страницу с ретраями.
//
// Стратегия ретраев:
//   - ошибка сети и 5xx — повторяем: это обычно временно;
//   - 429 — повторяем, но паузу назначает сайт (Retry-After), а не мы;
//   - остальные 4xx — НЕ повторяем: 404 и 403 от повтора не починятся,
//     а повтор — просто лишний стук в чужой сервер;
//   - 200 — готово.
//
// Между попытками — экспоненциальный backoff с jitter. Джиттер здесь не ради
// «стада» (парсер один), а чтобы регулярные прогоны не попадали в один и тот же
// такт с чужой нагрузкой на сайт.
func (f *Fetcher) fetch(ctx context.Context, rawURL, host string) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= f.opts.MaxAttempts; attempt++ {
		if attempt > 1 {
			f.log.Debug("повтор запроса", "url", rawURL, "attempt", attempt)
		}

		body, retryIn, err := f.do(ctx, rawURL, host, attempt)
		if err == nil {
			return body, nil
		}
		lastErr = err

		if retryIn < 0 {
			return nil, err // повторять бессмысленно
		}
		if attempt == f.opts.MaxAttempts {
			break
		}
		if retryIn == 0 {
			retryIn = backoff(f.opts.BaseBackoff, f.opts.MaxBackoff, attempt)
		}

		if err := sleepCtx(ctx, retryIn); err != nil {
			return nil, fmt.Errorf("ожидание перед повтором прервано: %w", err)
		}
	}

	return nil, fmt.Errorf("исчерпаны %d попыток: %w", f.opts.MaxAttempts, lastErr)
}

// do делает один запрос. retryIn: <0 — не повторять, 0 — повторить с backoff,
// >0 — повторить ровно через это время (столько попросил сайт).
func (f *Fetcher) do(ctx context.Context, rawURL, host string, attempt int) (body []byte, retryIn time.Duration, err error) {
	release, err := f.hosts.acquire(ctx, host)
	if err != nil {
		return nil, -1, fmt.Errorf("ожидание слота хоста %s: %w", host, err)
	}
	defer release()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, -1, fmt.Errorf("сборка запроса: %w", err)
	}
	req.Header.Set("User-Agent", f.opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "ru,en;q=0.9")

	resp, err := f.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, -1, fmt.Errorf("запрос прерван: %w", err)
		}
		return nil, 0, fmt.Errorf("сетевая ошибка: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, f.handle429(resp, host, attempt), fmt.Errorf("%w: %s", ErrTooManyRequests, rawURL)

	case resp.StatusCode >= 500:
		// Тело 5xx не читаем: оно нам не нужно, а соединение переиспользуется.
		return nil, 0, &httpError{StatusCode: resp.StatusCode, URL: rawURL}

	case resp.StatusCode != http.StatusOK:
		return nil, -1, &httpError{StatusCode: resp.StatusCode, URL: rawURL}
	}

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, 0, fmt.Errorf("чтение тела ответа: %w", err)
	}
	return body, -1, nil
}

// handle429 решает, сколько ждать после 429, и придерживает весь хост.
func (f *Fetcher) handle429(resp *http.Response, host string, attempt int) time.Duration {
	wait, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if !ok {
		// Заголовка нет или он мусорный — назначаем паузу сами тем же экспоненциальным
		// backoff с jitter, что и для сети/5xx: растёт с номером попытки, а не прыгает
		// сразу на максимум.
		wait = backoff(f.opts.BaseBackoff, f.opts.MaxBackoff, attempt)
		f.log.Warn("429 без Retry-After, ждём по backoff", "host", host, "attempt", attempt, "wait", wait)
	} else {
		f.log.Warn("429, сайт просит подождать", "host", host, "retry_after", wait)
	}

	// Придержать нужно весь хост, а не только этого воркера: иначе остальные
	// продолжат стучаться в сервер, который только что попросил притормозить.
	f.hosts.penalize(host, wait)

	if wait > f.opts.MaxRetryWait {
		// Сайт просит ждать дольше, чем мы готовы ждать внутри прогона. Держать слот
		// хоста весь этот срок нельзя, поэтому сдаёмся до следующего тика: хост уже
		// придержан через penalize, и следующий прогон сам выждет нужное время.
		f.log.Warn("429: пауза длиннее прогона, товар пропущен до следующего тика", "host", host, "retry_after", wait)
		return -1
	}
	return wait
}

// parseRetryAfter разбирает Retry-After. По RFC 9110 заголовок бывает двух видов:
// число секунд («120») или HTTP-дата («Wed, 21 Oct 2026 07:28:00 GMT»).
func parseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	if header == "" {
		return 0, false
	}

	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}

	if t, err := http.ParseTime(header); err == nil {
		d := t.Sub(now)
		if d < 0 {
			// Дата в прошлом означает «уже можно».
			return 0, true
		}
		return d, true
	}
	return 0, false
}

func backoff(base, max time.Duration, attempt int) time.Duration {
	d := base << (attempt - 1)
	if d > max || d <= 0 { // d <= 0 — защита от переполнения сдвигом
		d = max
	}

	// Half jitter: половина паузы фиксированная, половина случайная.
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
