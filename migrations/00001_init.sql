-- +goose Up
CREATE TABLE products (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT        NOT NULL,
    -- URL уникален: один товар — одна строка. Дубль означал бы двойной запрос
    -- к чужому сайту за той же ценой и двойное уведомление об изменении.
    url          TEXT        NOT NULL UNIQUE,
    css_selector TEXT        NOT NULL,
    is_active    BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE price_history (
    id         BIGSERIAL PRIMARY KEY,
    product_id BIGINT         NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    -- numeric, а не float: цена обязана храниться точно.
    -- 12 знаков хватает на 9 999 999 999,99 — потолок заведомо выше любых розничных цен.
    price      NUMERIC(12, 2) NOT NULL,
    currency   TEXT           NOT NULL,
    checked_at TIMESTAMPTZ    NOT NULL DEFAULT now()
);

-- Главный запрос парсера — «последняя цена товара»: ORDER BY checked_at DESC LIMIT 1.
-- Индекс в этом же порядке отдаёт её первой строкой, без сортировки всей истории.
CREATE INDEX idx_price_history_product_checked
    ON price_history (product_id, checked_at DESC);

-- +goose Down
DROP TABLE price_history;
DROP TABLE products;
