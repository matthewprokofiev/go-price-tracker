package exporter

import (
	"bytes"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

func TestWriteXLSX(t *testing.T) {
	rows := []domain.HistoryRow{
		{ProductName: "Кофеварка Bravo X200", Price: 1249950, Currency: "RUB", CheckedAt: time.Date(2026, 7, 18, 10, 30, 0, 0, time.UTC)},
		{ProductName: "Кофемолка Vento C5", Price: 320000, Currency: "RUB", CheckedAt: time.Date(2026, 7, 18, 11, 0, 0, 0, time.UTC)},
	}

	var buf bytes.Buffer
	if err := WriteXLSX(&buf, rows); err != nil {
		t.Fatalf("WriteXLSX(): %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("на выходе пустой файл")
	}

	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("файл не открывается excelize: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Лист назван "Цены" и он единственный (Sheet1 переименован, а не добавлен).
	sheets := f.GetSheetList()
	if len(sheets) != 1 || sheets[0] != sheetName {
		t.Fatalf("листы = %v, ожидался единственный %q", sheets, sheetName)
	}

	assertCell(t, f, "A1", "Товар")
	assertCell(t, f, "B1", "Цена")
	assertCell(t, f, "C1", "Валюта")
	assertCell(t, f, "D1", "Дата")

	assertCell(t, f, "A2", "Кофеварка Bravo X200")
	assertCell(t, f, "C2", "RUB")
	assertCell(t, f, "A3", "Кофемолка Vento C5")

	// Цена лежит числом, а не текстом «12 499,50 ₽»: иначе Excel не отсортирует и не
	// посчитает. Сырое значение — число; форматированное — с разделителями по стилю.
	assertRawCell(t, f, "B2", "12499.5")
	assertRawCell(t, f, "B3", "3200")
	assertCell(t, f, "B2", "12,499.50")
}

func TestWriteXLSXEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteXLSX(&buf, nil); err != nil {
		t.Fatalf("WriteXLSX(nil): %v", err)
	}

	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("пустой файл не открывается: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Даже без данных заголовок на месте — файл валидный и осмысленный.
	assertCell(t, f, "A1", "Товар")
}

func assertCell(t *testing.T, f *excelize.File, cell, want string) {
	t.Helper()
	got, err := f.GetCellValue(sheetName, cell)
	if err != nil {
		t.Fatalf("чтение %s: %v", cell, err)
	}
	if got != want {
		t.Errorf("%s = %q, ожидалось %q", cell, got, want)
	}
}

func assertRawCell(t *testing.T, f *excelize.File, cell, want string) {
	t.Helper()
	got, err := f.GetCellValue(sheetName, cell, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("чтение сырого %s: %v", cell, err)
	}
	if got != want {
		t.Errorf("сырое %s = %q, ожидалось %q", cell, got, want)
	}
}
