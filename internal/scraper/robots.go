package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

// robotsTTL: процесс живёт сутками, а robots.txt может измениться. Час — компромисс
// между «спросить разрешение заново» и «дёргать robots.txt на каждый товар».
const robotsTTL = time.Hour

// robotsMaxBody: robots.txt — текстовый файл на пару килобайт. Лимит защищает
// от сервера, который отдаёт под этим адресом что-то другое и бесконечное.
const robotsMaxBody = 512 << 10

type robotsCache struct {
	mu    sync.Mutex
	hosts map[string]*robotsHost

	client    *http.Client
	userAgent string
}

type robotsHost struct {
	// Мьютекс на хост, а не на весь кеш: параллельные воркеры одного хоста
	// подождут один запрос robots.txt вместо того, чтобы сделать пять,
	// а другие хосты при этом не блокируются.
	mu      sync.Mutex
	data    *robotstxt.RobotsData
	fetched time.Time
}

func newRobotsCache(client *http.Client, userAgent string) *robotsCache {
	return &robotsCache{
		hosts:     make(map[string]*robotsHost),
		client:    client,
		userAgent: userAgent,
	}
}

func (c *robotsCache) host(key string) *robotsHost {
	c.mu.Lock()
	defer c.mu.Unlock()

	h, ok := c.hosts[key]
	if !ok {
		h = &robotsHost{}
		c.hosts[key] = h
	}
	return h
}

// allowed сообщает, разрешает ли robots.txt хоста скачивать этот URL нашим User-Agent.
func (c *robotsCache) allowed(ctx context.Context, target *url.URL) (bool, error) {
	h := c.host(target.Scheme + "://" + target.Host)

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.data == nil || time.Since(h.fetched) > robotsTTL {
		data, err := c.fetch(ctx, target)
		if err != nil {
			return false, err
		}
		h.data, h.fetched = data, time.Now()
	}

	path := target.EscapedPath()
	if path == "" {
		path = "/"
	}
	if target.RawQuery != "" {
		path += "?" + target.RawQuery
	}
	return h.data.TestAgent(path, c.userAgent), nil
}

func (c *robotsCache) fetch(ctx context.Context, target *url.URL) (*robotstxt.RobotsData, error) {
	robotsURL := target.Scheme + "://" + target.Host + "/robots.txt"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("запрос robots.txt: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		// Сеть не ответила — считаем, что разрешения нет. Fail closed: молча парсить
		// сайт, не спросив robots.txt, хуже, чем пропустить прогон. Ошибка изолирована
		// на уровне товара, следующий тик попробует снова.
		return nil, fmt.Errorf("robots.txt недоступен (%s): %w", robotsURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, robotsMaxBody))
	if err != nil {
		return nil, fmt.Errorf("чтение robots.txt (%s): %w", robotsURL, err)
	}

	// FromStatusAndBytes реализует трактовку кодов из спеки Google: 4xx — «правил нет,
	// можно всё», 5xx — «сервер болеет, нельзя ничего».
	data, err := robotstxt.FromStatusAndBytes(resp.StatusCode, body)
	if err != nil {
		return nil, fmt.Errorf("разбор robots.txt (%s): %w", robotsURL, err)
	}
	return data, nil
}

// crawlDelay возвращает Crawl-delay, объявленный сайтом для нашего User-Agent.
// Если сайт просит ходить реже, чем наш дефолт, — слушаемся сайта.
func (c *robotsCache) crawlDelay(target *url.URL) time.Duration {
	h := c.host(target.Scheme + "://" + target.Host)

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.data == nil {
		return 0
	}
	if g := h.data.FindGroup(c.userAgent); g != nil {
		return g.CrawlDelay
	}
	return 0
}
