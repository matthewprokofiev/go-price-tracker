package scraper

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

func TestParsePrice(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantMinor    int64
		wantCurrency string
		wantErr      bool
	}{
		// Случаи из ТЗ.
		{name: "1 234,56 ₽", raw: "1 234,56 ₽", wantMinor: 123456, wantCurrency: "RUB"},
		{name: "1234.56 руб.", raw: "1234.56 руб.", wantMinor: 123456, wantCurrency: "RUB"},
		{name: "1 000 ₽", raw: "1 000 ₽", wantMinor: 100000, wantCurrency: "RUB"},
		{name: "пустая строка", raw: "", wantErr: true},
		{name: "только пробелы", raw: "   ", wantErr: true},
		{name: "мусор", raw: "цена по запросу", wantErr: true},
		{name: "отрицательная", raw: "-500 ₽", wantErr: true},
		{name: "отрицательная с юникод-минусом", raw: "−500 ₽", wantErr: true},

		// Неразрывные пробелы: именно так разделитель тысяч приезжает из HTML.
		{name: "неразрывный пробел", raw: "12 499,50 ₽", wantMinor: 1249950, wantCurrency: "RUB"},
		{name: "узкий неразрывный пробел", raw: "12 499,50 ₽", wantMinor: 1249950, wantCurrency: "RUB"},
		{name: "тонкий пробел", raw: "1 000 ₽", wantMinor: 100000, wantCurrency: "RUB"},

		{name: "перевод строки и табы вокруг", raw: "\n\t 899 ₽ \n", wantMinor: 89900, wantCurrency: "RUB"},
		{name: "с префиксом Цена:", raw: "Цена: 1 500 ₽", wantMinor: 150000, wantCurrency: "RUB"},
		{name: "ноль", raw: "0 ₽", wantMinor: 0, wantCurrency: "RUB"},
		{name: "одна цифра после запятой", raw: "12,5 ₽", wantMinor: 1250, wantCurrency: "RUB"},
		{name: "копейки нулевые", raw: "1 000,00 ₽", wantMinor: 100000, wantCurrency: "RUB"},

		// Разделители тысяч против десятичного разделителя.
		{name: "английский формат 1,234.56", raw: "1,234.56 $", wantMinor: 123456, wantCurrency: "USD"},
		{name: "немецкий формат 1.234,56", raw: "1.234,56 €", wantMinor: 123456, wantCurrency: "EUR"},
		{name: "запятая как разделитель тысяч 1,234", raw: "1,234 $", wantMinor: 123400, wantCurrency: "USD"},
		{name: "точка как разделитель тысяч 1.234", raw: "1.234 ₽", wantMinor: 123400, wantCurrency: "RUB"},
		{name: "миллион с разделителями", raw: "1 234 567,89 ₽", wantMinor: 123456789, wantCurrency: "RUB"},

		// Валюты.
		{name: "фунт — как на books.toscrape", raw: "£51.77", wantMinor: 5177, wantCurrency: "GBP"},
		{name: "доллар", raw: "$1,999.00", wantMinor: 199900, wantCurrency: "USD"},
		{name: "евро", raw: "500,00 €", wantMinor: 50000, wantCurrency: "EUR"},
		{name: "код валюты словом", raw: "1500.00 USD", wantMinor: 150000, wantCurrency: "USD"},
		{name: "без валюты — дефолт RUB", raw: "1 500", wantMinor: 150000, wantCurrency: "RUB"},
		{name: "рубли словом", raw: "1500 рублей", wantMinor: 150000, wantCurrency: "RUB"},

		// Отказы.
		{name: "диапазон цен", raw: "1000-2000 ₽", wantErr: true},
		{name: "неоднозначный разделитель", raw: "1,2345 ₽", wantErr: true},
		{name: "нет целой части", raw: ",56 ₽", wantErr: true},
		{name: "выше потолка numeric(12,2)", raw: "99999999999999 ₽", wantErr: true},
		{name: "только валюта", raw: "₽", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePrice(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePrice(%q) = %+v, ожидалась ошибка", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePrice(%q): неожиданная ошибка: %v", tt.raw, err)
			}
			if got.Price.Minor() != tt.wantMinor {
				t.Errorf("ParsePrice(%q).Price = %d копеек, ожидалось %d", tt.raw, got.Price.Minor(), tt.wantMinor)
			}
			if got.Currency != tt.wantCurrency {
				t.Errorf("ParsePrice(%q).Currency = %q, ожидалось %q", tt.raw, got.Currency, tt.wantCurrency)
			}
		})
	}
}

// Извлечение цены с сохранённой фикстуры — без единого сетевого запроса.
func TestExtractPriceFromFixture(t *testing.T) {
	doc := loadFixture(t, "../../testdata/page.html")

	got, err := ExtractPrice(doc, ".product-card__purchase .product-card__price")
	if err != nil {
		t.Fatalf("ExtractPrice(): %v", err)
	}
	if want := domain.Money(1249950); got.Price != want {
		t.Errorf("Price = %d, ожидалось %d", got.Price, want)
	}
	if got.Currency != "RUB" {
		t.Errorf("Currency = %q, ожидалось RUB", got.Currency)
	}
	if s := got.Price.Format(got.Currency); s != "12 499,50 ₽" {
		t.Errorf("отформатированная цена = %q", s)
	}
}

// Тот же класс цены есть в блоке «похожие товары»: неотскоупленный селектор
// цепляет и карточку, и рекомендации. Проверяем, что берётся именно первое
// совпадение — цена самого товара, а не соседнего.
func TestExtractPriceUnscopedSelectorTakesFirstMatch(t *testing.T) {
	doc := loadFixture(t, "../../testdata/page.html")

	got, err := ExtractPrice(doc, ".product-card__price")
	if err != nil {
		t.Fatalf("ExtractPrice(): %v", err)
	}
	if want := domain.Money(1249950); got.Price != want {
		t.Errorf("Price = %d, ожидалось %d (цена карточки, а не рекомендации)", got.Price, want)
	}
}

func TestExtractPriceOldPriceSelector(t *testing.T) {
	doc := loadFixture(t, "../../testdata/page.html")

	got, err := ExtractPrice(doc, ".product-card__price-old")
	if err != nil {
		t.Fatalf("ExtractPrice(): %v", err)
	}
	if want := domain.Money(1599000); got.Price != want {
		t.Errorf("Price = %d, ожидалось %d", got.Price, want)
	}
}

// Страница жива, но цены нет: селектор не нашёлся -> ErrPriceNotFound, а не 0.
func TestExtractPriceMissing(t *testing.T) {
	doc := loadFixture(t, "../../testdata/page_no_price.html")

	_, err := ExtractPrice(doc, ".product-card__purchase .product-card__price")
	if !errors.Is(err, domain.ErrPriceNotFound) {
		t.Fatalf("ожидалась ErrPriceNotFound, получено: %v", err)
	}
}

// Селектор нашёлся, но внутри текст без цены — тоже ошибка, а не нулевая цена.
func TestExtractPriceNonNumericText(t *testing.T) {
	doc := loadFixture(t, "../../testdata/page_no_price.html")

	_, err := ExtractPrice(doc, ".product-card__availability")
	if err == nil {
		t.Fatal("ожидалась ошибка на тексте «Снят с продажи»")
	}
}

func loadFixture(t *testing.T, path string) *goquery.Document {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение фикстуры %s: %v", path, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("разбор фикстуры %s: %v", path, err)
	}
	return doc
}
