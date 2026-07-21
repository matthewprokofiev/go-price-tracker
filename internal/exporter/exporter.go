package exporter

import (
	"fmt"
	"io"

	"github.com/xuri/excelize/v2"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

const sheetName = "Цены"

var headers = []string{"Товар", "Цена", "Валюта", "Дата"}

// WriteXLSX пишет историю цен в xlsx в переданный writer. Writer, а не путь к файлу:
// так экспорт одинаково ложится и в файл на диске, и в HTTP-ответ, и в тест через буфер.
func WriteXLSX(w io.Writer, rows []domain.HistoryRow) error {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	// NewFile создаёт лист "Sheet1"; переименовываем его, а не заводим второй,
	// иначе в книге останется лишний пустой лист.
	if err := f.SetSheetName("Sheet1", sheetName); err != nil {
		return fmt.Errorf("переименование листа: %w", err)
	}

	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
	})
	if err != nil {
		return fmt.Errorf("стиль заголовка: %w", err)
	}

	// Цена и дата хранятся как числа с форматом отображения, а не как текст: так в Excel
	// работают сортировка по цене, фильтры и арифметика. Текстовая «1 000 ₽» их сломала бы.
	priceStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: strptr("#,##0.00")})
	if err != nil {
		return fmt.Errorf("стиль цены: %w", err)
	}
	dateStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: strptr("yyyy-mm-dd hh:mm")})
	if err != nil {
		return fmt.Errorf("стиль даты: %w", err)
	}

	for i, h := range headers {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return fmt.Errorf("координата заголовка %d: %w", i, err)
		}
		if err := f.SetCellValue(sheetName, cell, h); err != nil {
			return fmt.Errorf("запись заголовка %q: %w", h, err)
		}
	}
	if err := f.SetCellStyle(sheetName, "A1", "D1", headerStyle); err != nil {
		return fmt.Errorf("применение стиля заголовка: %w", err)
	}

	for i, row := range rows {
		r := i + 2 // строка 1 — заголовок

		if err := f.SetCellValue(sheetName, cell(1, r), row.ProductName); err != nil {
			return fmt.Errorf("запись названия (строка %d): %w", r, err)
		}
		// Money — копейки; в рубли для отображения переводим здесь, единственный раз,
		// делением на 100 во float. Точность на двух знаках не страдает.
		if err := f.SetCellValue(sheetName, cell(2, r), float64(row.Price.Minor())/100); err != nil {
			return fmt.Errorf("запись цены (строка %d): %w", r, err)
		}
		if err := f.SetCellValue(sheetName, cell(3, r), row.Currency); err != nil {
			return fmt.Errorf("запись валюты (строка %d): %w", r, err)
		}
		if err := f.SetCellValue(sheetName, cell(4, r), row.CheckedAt); err != nil {
			return fmt.Errorf("запись даты (строка %d): %w", r, err)
		}
	}

	if len(rows) > 0 {
		last := len(rows) + 1
		if err := f.SetCellStyle(sheetName, "B2", cell(2, last), priceStyle); err != nil {
			return fmt.Errorf("формат цен: %w", err)
		}
		if err := f.SetCellStyle(sheetName, "D2", cell(4, last), dateStyle); err != nil {
			return fmt.Errorf("формат дат: %w", err)
		}
	}

	setColumnWidths(f)

	// Шапка остаётся на месте при прокрутке — на истории в сотни строк это ощутимо удобнее.
	if err := f.SetPanes(sheetName, &excelize.Panes{
		Freeze:      true,
		YSplit:      1,
		TopLeftCell: "A2",
		ActivePane:  "bottomLeft",
	}); err != nil {
		return fmt.Errorf("закрепление шапки: %w", err)
	}

	if err := f.Write(w); err != nil {
		return fmt.Errorf("запись xlsx: %w", err)
	}
	return nil
}

func setColumnWidths(f *excelize.File) {
	// Ошибки ширины колонок глотаем осознанно: это косметика, из-за неё незачем
	// проваливать экспорт с уже готовыми данными.
	_ = f.SetColWidth(sheetName, "A", "A", 40)
	_ = f.SetColWidth(sheetName, "B", "B", 14)
	_ = f.SetColWidth(sheetName, "C", "C", 8)
	_ = f.SetColWidth(sheetName, "D", "D", 18)
}

func cell(col, row int) string {
	// CoordinatesToCellName падает только на неположительных координатах; здесь
	// они всегда положительные, поэтому ошибку можно не тащить наружу.
	name, _ := excelize.CoordinatesToCellName(col, row)
	return name
}

func strptr(s string) *string { return &s }
