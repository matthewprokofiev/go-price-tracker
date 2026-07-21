package storage

import (
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

func TestMoneyToNumericRoundTrip(t *testing.T) {
	values := []domain.Money{0, 1, 50, 100, 99999, 100000, 1249950, -50000, 999999999999}

	for _, want := range values {
		t.Run(want.String(), func(t *testing.T) {
			got, err := numericToMoney(moneyToNumeric(want))
			if err != nil {
				t.Fatalf("numericToMoney(moneyToNumeric(%d)): %v", want, err)
			}
			if got != want {
				t.Errorf("round-trip %d = %d", want, got)
			}
		})
	}
}

func TestNumericToMoney(t *testing.T) {
	num := func(i int64, exp int32) pgtype.Numeric {
		return pgtype.Numeric{Int: big.NewInt(i), Exp: exp, Valid: true}
	}

	tests := []struct {
		name    string
		in      pgtype.Numeric
		want    domain.Money
		wantErr bool
	}{
		// Postgres отдаёт numeric(12,2) с Exp=-2, но нормализация возможна любая —
		// проверяем, что сдвиг считается честно в обе стороны.
		{name: "Exp=-2, как отдаёт numeric(12,2)", in: num(124999, -2), want: 124999},
		{name: "Exp=0 — целые рубли", in: num(1000, 0), want: 100000},
		{name: "Exp=-1 — десятые", in: num(12345, -1), want: 123450},
		{name: "Exp=2 — сотни", in: num(12, 2), want: 120000},
		{name: "Exp=-2 отрицательное", in: num(-5000, -2), want: -5000},
		{name: "ноль", in: num(0, -2), want: 0},
		// Доли копейки округлять молча нельзя: это тихая потеря денег.
		{name: "доли копейки — ошибка", in: num(123456, -4), wantErr: true},
		{name: "NULL", in: pgtype.Numeric{}, wantErr: true},
		{name: "NaN", in: pgtype.Numeric{Int: big.NewInt(1), NaN: true, Valid: true}, wantErr: true},
		{name: "бесконечность", in: pgtype.Numeric{Int: big.NewInt(1), InfinityModifier: pgtype.Infinity, Valid: true}, wantErr: true},
		{name: "Int=nil", in: pgtype.Numeric{Valid: true}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := numericToMoney(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("numericToMoney() = %d, ожидалась ошибка", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("numericToMoney(): неожиданная ошибка: %v", err)
			}
			if got != tt.want {
				t.Errorf("numericToMoney() = %d, ожидалось %d", got, tt.want)
			}
		})
	}
}

// Число, которое не влезает в int64, должно дать ошибку, а не молча переполниться.
func TestNumericToMoneyOverflow(t *testing.T) {
	huge, _ := new(big.Int).SetString("99999999999999999999999999", 10)

	if got, err := numericToMoney(pgtype.Numeric{Int: huge, Exp: 0, Valid: true}); err == nil {
		t.Fatalf("numericToMoney(огромное) = %d, ожидалась ошибка переполнения", got)
	}
}
