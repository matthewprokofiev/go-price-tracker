package domain

import (
	"fmt"
	"strings"
)

// Money — цена в минорных единицах валюты (копейках, центах).
// Деньги принципиально не float64: 0.1+0.2 != 0.3, а цены складывают, сравнивают
// и хранят годами. int64 в копейках даёт точное сравнение «цена изменилась»
// и ровно ложится на numeric(12,2) в Postgres.
type Money int64

const minorUnits = 100

func (m Money) Minor() int64 { return int64(m) }

// String форматирует цену по-русски: разделитель тысяч — пробел, дробная часть — через
// запятую и только когда она ненулевая («1 000», но «12 499,50»).
func (m Money) String() string {
	neg := m < 0
	if neg {
		m = -m
	}

	whole := int64(m) / minorUnits
	frac := int64(m) % minorUnits

	out := groupThousands(whole)
	if frac != 0 {
		out = fmt.Sprintf("%s,%02d", out, frac)
	}
	if neg {
		out = "-" + out
	}
	return out
}

// Format добавляет к цене символ валюты: «12 499,50 ₽».
func (m Money) Format(currency string) string {
	return m.String() + " " + CurrencySymbol(currency)
}

func CurrencySymbol(code string) string {
	switch strings.ToUpper(code) {
	case "RUB":
		return "₽"
	case "USD":
		return "$"
	case "EUR":
		return "€"
	default:
		return code
	}
}

func groupThousands(n int64) string {
	digits := fmt.Sprintf("%d", n)

	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
