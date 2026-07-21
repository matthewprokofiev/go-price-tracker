package storage

import (
	"context"
	"fmt"
)

// Сид указывает на books.toscrape.com — учебную песочницу, сделанную авторами Scrapy
// специально для тренировки парсеров. Брать боевой магазин ради демо в портфолио
// нечестно: там парсинг регулируется ToS, а нагрузка достаётся живому бизнесу.
// Цены на песочнице статичные, поэтому уведомление в демо проще всего показать,
// подменив цену в БД руками (см. README).
var seedProducts = []struct {
	name        string
	url         string
	cssSelector string
}{
	{
		name:        "A Light in the Attic",
		url:         "https://books.toscrape.com/catalogue/a-light-in-the-attic_1000/index.html",
		cssSelector: ".product_main .price_color",
	},
	{
		name:        "Tipping the Velvet",
		url:         "https://books.toscrape.com/catalogue/tipping-the-velvet_999/index.html",
		cssSelector: ".product_main .price_color",
	},
	{
		name:        "Soumission",
		url:         "https://books.toscrape.com/catalogue/soumission_998/index.html",
		cssSelector: ".product_main .price_color",
	},
	{
		name:        "Sharp Objects",
		url:         "https://books.toscrape.com/catalogue/sharp-objects_997/index.html",
		cssSelector: ".product_main .price_color",
	},
	{
		name:        "Sapiens: A Brief History of Humankind",
		url:         "https://books.toscrape.com/catalogue/sapiens-a-brief-history-of-humankind_996/index.html",
		cssSelector: ".product_main .price_color",
	},
}

// Seed идемпотентен по уникальному url: повторный старт не плодит дубли и не трогает
// товары, которым уже поменяли селектор или сняли is_active руками.
func (s *Storage) Seed(ctx context.Context) error {
	const query = `
		INSERT INTO products (name, url, css_selector)
		VALUES ($1, $2, $3)
		ON CONFLICT (url) DO NOTHING`

	var added int64
	for _, p := range seedProducts {
		tag, err := s.pool.Exec(ctx, query, p.name, p.url, p.cssSelector)
		if err != nil {
			return fmt.Errorf("сид товара %q: %w", p.name, err)
		}
		added += tag.RowsAffected()
	}

	s.log.Info("сид товаров выполнен", "added", added, "total", len(seedProducts))
	return nil
}
