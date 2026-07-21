package domain

import "testing"

func TestClassify(t *testing.T) {
	rub := func(minor int64) Quote { return Quote{Price: Money(minor), Currency: "RUB"} }

	tests := []struct {
		name       string
		previous   *Quote
		current    Quote
		want       ChangeKind
		wantRecord bool
		wantNotify bool
	}{
		{
			name: "первое наблюдение — пишем, но не уведомляем",
			// previous == nil: в price_history по товару ещё пусто.
			previous: nil, current: rub(100000),
			want: KindFirstSeen, wantRecord: true, wantNotify: false,
		},
		{
			name:     "цена не изменилась — не пишем и не уведомляем",
			previous: ptr(rub(100000)), current: rub(100000),
			want: KindUnchanged, wantRecord: false, wantNotify: false,
		},
		{
			name:     "цена упала",
			previous: ptr(rub(100000)), current: rub(90000),
			want: KindChanged, wantRecord: true, wantNotify: true,
		},
		{
			name:     "цена выросла",
			previous: ptr(rub(90000)), current: rub(100000),
			want: KindChanged, wantRecord: true, wantNotify: true,
		},
		{
			name: "изменение на одну копейку — тоже изменение",
			// Ради этого случая деньги и не float64: 999.99 против 1000.00
			// на float сравнение могло бы дать «не изменилось».
			previous: ptr(rub(99999)), current: rub(100000),
			want: KindChanged, wantRecord: true, wantNotify: true,
		},
		{
			name:     "сумма та же, валюта другая — это изменение",
			previous: ptr(Quote{Price: 10000, Currency: "RUB"}), current: Quote{Price: 10000, Currency: "USD"},
			want: KindChanged, wantRecord: true, wantNotify: true,
		},
		{
			name:     "нулевая цена не путается с отсутствием предыдущей",
			previous: ptr(rub(0)), current: rub(0),
			want: KindUnchanged, wantRecord: false, wantNotify: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.previous, tt.current)
			if got != tt.want {
				t.Fatalf("Classify() = %v, ожидалось %v", got, tt.want)
			}
			if got.ShouldRecord() != tt.wantRecord {
				t.Errorf("ShouldRecord() = %v, ожидалось %v", got.ShouldRecord(), tt.wantRecord)
			}
			if got.ShouldNotify() != tt.wantNotify {
				t.Errorf("ShouldNotify() = %v, ожидалось %v", got.ShouldNotify(), tt.wantNotify)
			}
		})
	}
}

func TestMoneyString(t *testing.T) {
	tests := []struct {
		minor int64
		want  string
	}{
		{minor: 0, want: "0"},
		{minor: 50, want: "0,50"},
		{minor: 99, want: "0,99"},
		{minor: 100, want: "1"},
		{minor: 90000, want: "900"},
		{minor: 100000, want: "1 000"},
		{minor: 1249950, want: "12 499,50"},
		{minor: 123456, want: "1 234,56"},
		{minor: 100000000, want: "1 000 000"},
		{minor: -50000, want: "-500"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := Money(tt.minor).String(); got != tt.want {
				t.Errorf("Money(%d).String() = %q, ожидалось %q", tt.minor, got, tt.want)
			}
		})
	}
}

func TestMoneyFormat(t *testing.T) {
	tests := []struct {
		minor    int64
		currency string
		want     string
	}{
		{minor: 100000, currency: "RUB", want: "1 000 ₽"},
		{minor: 1249950, currency: "RUB", want: "12 499,50 ₽"},
		{minor: 199900, currency: "USD", want: "1 999 $"},
		{minor: 50000, currency: "EUR", want: "500 €"},
		{minor: 50000, currency: "GBP", want: "500 GBP"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := Money(tt.minor).Format(tt.currency); got != tt.want {
				t.Errorf("Money(%d).Format(%q) = %q, ожидалось %q", tt.minor, tt.currency, got, tt.want)
			}
		})
	}
}

func ptr(q Quote) *Quote { return &q }
