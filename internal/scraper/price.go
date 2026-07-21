package scraper

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

// defaultCurrency используется, когда на странице нет ни символа, ни кода валюты.
// Допущение осознанное: цель проекта — российские магазины. Валюта, найденная явно
// (₽, $, €, £), всегда важнее дефолта.
const defaultCurrency = "RUB"

// maxPriceMinor соответствует потолку колонки numeric(12,2) — 9 999 999 999,99.
// Проверка здесь, а не в БД: лучше поймать мусор (склеенный телефон, артикул)
// на разборе строки, чем получить ошибку вставки на другом конце программы.
const maxPriceMinor = 999_999_999_999

// Порядок важен: маркеры проверяются сверху вниз, первый найденный побеждает.
var currencyMarkers = []struct{ marker, code string }{
	{"₽", "RUB"},
	{"руб", "RUB"},
	{"rub", "RUB"},
	{"$", "USD"},
	{"usd", "USD"},
	{"€", "EUR"},
	{"eur", "EUR"},
	{"£", "GBP"},
	{"gbp", "GBP"},
}

// ExtractPrice достаёт цену из документа по CSS-селектору.
func ExtractPrice(doc *goquery.Document, selector string) (domain.Quote, error) {
	sel := doc.Find(selector)
	if sel.Length() == 0 {
		return domain.Quote{}, fmt.Errorf("селектор %q: %w", selector, domain.ErrPriceNotFound)
	}

	// Берётся первое совпадение: на реальных страницах класс цены часто повторяется
	// в блоках «похожие товары», и жадный селектор хватал бы чужую цену.
	// Ответственность за точность — на селекторе в БД, он для того и вынесен в конфиг.
	raw := strings.TrimSpace(sel.First().Text())

	quote, err := ParsePrice(raw)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("селектор %q нашёл %q: %w", selector, raw, err)
	}
	return quote, nil
}

// ParsePrice разбирает цену в том виде, в каком её пишут на витрине:
// «1 234,56 ₽», «1234.56 руб.», «£51.77».
func ParsePrice(raw string) (domain.Quote, error) {
	if strings.TrimSpace(raw) == "" {
		return domain.Quote{}, fmt.Errorf("%w: пустая строка", domain.ErrPriceNotFound)
	}

	amount, err := parseAmount(raw)
	if err != nil {
		return domain.Quote{}, err
	}
	return domain.Quote{Price: amount, Currency: detectCurrency(raw)}, nil
}

func detectCurrency(raw string) string {
	lower := strings.ToLower(raw)
	for _, c := range currencyMarkers {
		if strings.Contains(lower, c.marker) {
			return c.code
		}
	}
	return defaultCurrency
}

func parseAmount(raw string) (domain.Money, error) {
	// Юникодный минус U+2212 витрины иногда ставят вместо ASCII '-'. Нормализуем до
	// фильтра, иначе он выпал бы как «мусор» и «−100» разобралось бы как +100.
	raw = strings.ReplaceAll(raw, "−", "-")

	// Всё, кроме цифр и разделителей, выбрасывается: вокруг цены всегда мусор —
	// «Цена:», значок валюты, неразрывные пробелы, перевод строки.
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9', r == '.', r == ',', r == '-':
			b.WriteRune(r)
		}
	}
	// Точка из «руб.» — не часть числа: без обрезки справа «1234.56 руб.» превратился бы
	// в «1234.56.» и разбор сломался. Слева не трогаем: «,56» — это отсутствие целой
	// части (мусор), и падение на нём правильное.
	s := strings.TrimRight(b.String(), ".,")

	if !strings.ContainsFunc(s, func(r rune) bool { return r >= '0' && r <= '9' }) {
		return 0, fmt.Errorf("%w: в %q нет цифр", domain.ErrPriceNotFound, raw)
	}

	if i := strings.IndexByte(s, '-'); i >= 0 {
		if i == 0 {
			return 0, fmt.Errorf("отрицательная цена: %q", raw)
		}
		// «1000-2000» — вилка цен. Молча взять любую границу хуже, чем честно упасть:
		// в истории появилась бы цена, которой на витрине нет.
		return 0, fmt.Errorf("похоже на диапазон цен, а не на цену: %q", raw)
	}

	whole, frac, err := splitDecimal(s)
	if err != nil {
		return 0, fmt.Errorf("%w (%q)", err, raw)
	}

	rubles, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("не удалось разобрать целую часть %q: %w", raw, err)
	}
	kopeks, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("не удалось разобрать дробную часть %q: %w", raw, err)
	}

	if rubles > (maxPriceMinor-kopeks)/100 {
		return 0, fmt.Errorf("цена %q превышает потолок numeric(12,2)", raw)
	}
	return domain.Money(rubles*100 + kopeks), nil
}

// splitDecimal разделяет «1 234,56» на целую и дробную часть.
//
// Разделители неоднозначны: «1,234» — это 1234 по-английски и 1.234 по-русски.
// Решает количество цифр после последнего разделителя: 1–2 цифры — это копейки
// («12,5», «12,50»), ровно 3 — разделитель тысяч («1,234» → 1234), потому что
// цен с тремя знаками после запятой в рознице не бывает. Всё остальное
// («1,2345») неоднозначно, и лучше упасть, чем угадать неправильно.
func splitDecimal(s string) (whole, frac string, err error) {
	last := strings.LastIndexAny(s, ".,")
	if last < 0 {
		return s, "00", nil
	}

	after := s[last+1:]
	switch len(after) {
	case 1, 2:
		whole = stripSeparators(s[:last])
		frac = after
		if len(frac) == 1 {
			frac += "0" // «12,5» — это 12 рублей 50 копеек, а не 5.
		}
	case 3:
		whole = stripSeparators(s)
		frac = "00"
	default:
		return "", "", fmt.Errorf("неоднозначный разделитель: %d цифр после %q", len(after), s[last:last+1])
	}

	if whole == "" {
		return "", "", fmt.Errorf("нет целой части")
	}
	return whole, frac, nil
}

func stripSeparators(s string) string {
	return strings.NewReplacer(".", "", ",", "").Replace(s)
}
