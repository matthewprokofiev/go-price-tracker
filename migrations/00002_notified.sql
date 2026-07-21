-- +goose Up
-- notified = «по этой строке уведомление больше не требуется». Изменение цены пишется
-- со notified=FALSE и переводится в TRUE только после успешной отправки в Telegram.
-- Это разрывает связку «записали цену → потеряли уведомление при сбое сети»: недоставленное
-- добирается на следующем прогоне, а не теряется навсегда.
-- DEFAULT TRUE: существующие строки и первые наблюдения считаются обработанными —
-- рассылать задним числом старую историю не нужно.
ALTER TABLE price_history ADD COLUMN notified BOOLEAN NOT NULL DEFAULT TRUE;

-- Частичный индекс под запрос добора: недоставленных строк единицы, полный индекс избыточен.
CREATE INDEX idx_price_history_unnotified
    ON price_history (product_id, checked_at) WHERE notified = FALSE;

-- +goose Down
DROP INDEX idx_price_history_unnotified;
ALTER TABLE price_history DROP COLUMN notified;
