package domain

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("не найдено")
	// ErrPriceNotFound: страница отдалась, но цены в ней нет — сменилась вёрстка
	// или товар сняли с продажи. Это ошибка товара, а не сети: ретраить бессмысленно.
	ErrPriceNotFound = errors.New("цена не найдена на странице")
)

type Product struct {
	ID          int64
	Name        string
	URL         string
	CSSSelector string
	IsActive    bool
	CreatedAt   time.Time
}

type PricePoint struct {
	ID        int64
	ProductID int64
	Price     Money
	Currency  string
	CheckedAt time.Time
}

// HistoryRow — строка выгрузки: история цены вместе с именем товара.
type HistoryRow struct {
	ProductName string
	Price       Money
	Currency    string
	CheckedAt   time.Time
}

// Quote — цена, снятая со страницы за один прогон. Валюта едет вместе с суммой:
// сравнивать 100 ₽ со 100 $ как «не изменилось» было бы багом.
type Quote struct {
	Price    Money
	Currency string
}

// PendingNotification — записанное изменение цены, о котором ещё не удалось сообщить.
// Собирается из строки price_history и предыдущей цены того же товара, чтобы добор
// уведомления слал то же «было … стало …», что и не дошедшее сообщение.
type PendingNotification struct {
	PriceID int64
	Product Product
	Old     Quote
	New     Quote
}

type ChangeKind int

const (
	// KindFirstSeen — предыдущей цены нет: первое наблюдение за товаром.
	KindFirstSeen ChangeKind = iota
	KindUnchanged
	KindChanged
)

// Classify решает, что делать с только что снятой ценой.
// previous == nil означает, что в price_history по товару ещё ничего нет.
func Classify(previous *Quote, current Quote) ChangeKind {
	switch {
	case previous == nil:
		return KindFirstSeen
	case *previous == current:
		return KindUnchanged
	default:
		return KindChanged
	}
}

// ShouldRecord: price_history — журнал изменений, а не журнал опросов. Писать строку
// на каждую проверку значило бы за сутки нагенерить сотни одинаковых цен,
// сквозь которые не видно самих изменений.
func (k ChangeKind) ShouldRecord() bool { return k != KindUnchanged }

// ShouldNotify: на первом наблюдении уведомлять не о чем — «было ничего, стало 1000»
// не сообщение, а шум. При первом запуске демо это ожидаемая тишина: пишем базовую цену.
func (k ChangeKind) ShouldNotify() bool { return k == KindChanged }

func (k ChangeKind) String() string {
	switch k {
	case KindFirstSeen:
		return "first_seen"
	case KindUnchanged:
		return "unchanged"
	case KindChanged:
		return "changed"
	default:
		return "unknown"
	}
}
