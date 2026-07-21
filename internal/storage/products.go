package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

func (s *Storage) ActiveProducts(ctx context.Context) ([]domain.Product, error) {
	const query = `
		SELECT id, name, url, css_selector, is_active, created_at
		FROM products
		WHERE is_active = TRUE
		ORDER BY id`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("запрос активных товаров: %w", err)
	}
	defer rows.Close()

	var products []domain.Product
	for rows.Next() {
		var p domain.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.URL, &p.CSSSelector, &p.IsActive, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("чтение товара: %w", err)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("перебор товаров: %w", err)
	}
	return products, nil
}

// LastQuote возвращает последнюю известную цену товара. nil без ошибки означает,
// что товар ещё ни разу не проверялся — это нормальное состояние, а не сбой.
func (s *Storage) LastQuote(ctx context.Context, productID int64) (*domain.Quote, error) {
	const query = `
		SELECT price, currency
		FROM price_history
		WHERE product_id = $1
		ORDER BY checked_at DESC
		LIMIT 1`

	var (
		price    pgtype.Numeric
		currency string
	)
	err := s.pool.QueryRow(ctx, query, productID).Scan(&price, &currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("запрос последней цены товара %d: %w", productID, err)
	}

	money, err := numericToMoney(price)
	if err != nil {
		return nil, fmt.Errorf("последняя цена товара %d: %w", productID, err)
	}
	return &domain.Quote{Price: money, Currency: currency}, nil
}

// AddPrice записывает цену и возвращает id строки. notified=false означает «уведомление
// по этой строке ещё не отправлено» — так изменение, о котором не удалось сообщить,
// не теряется: его подберёт PendingNotifications на следующем прогоне.
func (s *Storage) AddPrice(ctx context.Context, productID int64, q domain.Quote, notified bool) (int64, error) {
	const query = `
		INSERT INTO price_history (product_id, price, currency, notified)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	var id int64
	err := s.pool.QueryRow(ctx, query, productID, moneyToNumeric(q.Price), q.Currency, notified).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("запись цены товара %d: %w", productID, err)
	}
	return id, nil
}

func (s *Storage) MarkNotified(ctx context.Context, priceID int64) error {
	if _, err := s.pool.Exec(ctx, `UPDATE price_history SET notified = TRUE WHERE id = $1`, priceID); err != nil {
		return fmt.Errorf("отметка уведомления %d: %w", priceID, err)
	}
	return nil
}

// PendingNotifications возвращает изменения цены, о которых не удалось сообщить.
// Предыдущая цена берётся оконным LAG в порядке checked_at, чтобы восстановить «было».
// Строки без предшественника (первые наблюдения) под условие notified=FALSE не попадают —
// они пишутся сразу notified=TRUE, — поэтому INNER-фильтр по prev их корректно отсекает.
func (s *Storage) PendingNotifications(ctx context.Context) ([]domain.PendingNotification, error) {
	const query = `
		WITH ranked AS (
			SELECT
				h.id, h.product_id, h.price, h.currency, h.notified,
				LAG(h.price)    OVER w AS prev_price,
				LAG(h.currency) OVER w AS prev_currency
			FROM price_history h
			WINDOW w AS (PARTITION BY h.product_id ORDER BY h.checked_at)
		)
		SELECT r.id, p.id, p.name, p.url, p.css_selector,
		       r.prev_price, r.prev_currency, r.price, r.currency
		FROM ranked r
		JOIN products p ON p.id = r.product_id
		WHERE r.notified = FALSE AND r.prev_price IS NOT NULL
		ORDER BY r.id`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("запрос недоставленных уведомлений: %w", err)
	}
	defer rows.Close()

	var pending []domain.PendingNotification
	for rows.Next() {
		var (
			n                  domain.PendingNotification
			oldPrice, newPrice pgtype.Numeric
			oldCurrency        string
		)
		if err := rows.Scan(
			&n.PriceID, &n.Product.ID, &n.Product.Name, &n.Product.URL, &n.Product.CSSSelector,
			&oldPrice, &oldCurrency, &newPrice, &n.New.Currency,
		); err != nil {
			return nil, fmt.Errorf("чтение недоставленного уведомления: %w", err)
		}

		if n.Old.Price, err = numericToMoney(oldPrice); err != nil {
			return nil, fmt.Errorf("прошлая цена уведомления %d: %w", n.PriceID, err)
		}
		if n.New.Price, err = numericToMoney(newPrice); err != nil {
			return nil, fmt.Errorf("новая цена уведомления %d: %w", n.PriceID, err)
		}
		n.Old.Currency = oldCurrency
		pending = append(pending, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("перебор недоставленных уведомлений: %w", err)
	}
	return pending, nil
}

// History отдаёт всю историю с именами товаров — ровно то, что уходит в выгрузку.
// Джойн живёт здесь, а не в экспортёре: экспортёр не должен знать про схему БД.
func (s *Storage) History(ctx context.Context) ([]domain.HistoryRow, error) {
	const query = `
		SELECT p.name, h.price, h.currency, h.checked_at
		FROM price_history h
		JOIN products p ON p.id = h.product_id
		ORDER BY p.name, h.checked_at`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("запрос истории цен: %w", err)
	}
	defer rows.Close()

	var history []domain.HistoryRow
	for rows.Next() {
		var (
			row   domain.HistoryRow
			price pgtype.Numeric
		)
		if err := rows.Scan(&row.ProductName, &price, &row.Currency, &row.CheckedAt); err != nil {
			return nil, fmt.Errorf("чтение строки истории: %w", err)
		}

		row.Price, err = numericToMoney(price)
		if err != nil {
			return nil, fmt.Errorf("цена в истории товара %q: %w", row.ProductName, err)
		}
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("перебор истории цен: %w", err)
	}
	return history, nil
}
