package storage

import (
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/matveiprokofev/go-price-tracker/internal/domain"
)

// pgtype.Numeric хранит значение как Int * 10^Exp. Money — это копейки, то есть
// то же значение при Exp = -2, поэтому перевод в обе стороны точный и идёт через
// big.Int: любой промежуточный float здесь означал бы потерянную копейку.

func moneyToNumeric(m domain.Money) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(m.Minor()), Exp: -2, Valid: true}
}

func numericToMoney(n pgtype.Numeric) (domain.Money, error) {
	switch {
	case !n.Valid:
		return 0, fmt.Errorf("цена NULL")
	case n.NaN:
		return 0, fmt.Errorf("цена NaN")
	case n.InfinityModifier != pgtype.Finite:
		return 0, fmt.Errorf("цена бесконечна")
	case n.Int == nil:
		return 0, fmt.Errorf("цена без значения")
	}

	// Копейки = Int * 10^(Exp+2). Колонка numeric(12,2) всегда отдаёт Exp = -2,
	// но полагаться на это не стоит: сдвиг считается честно в обе стороны.
	v := new(big.Int).Set(n.Int)
	shift := n.Exp + 2

	if shift >= 0 {
		v.Mul(v, pow10(shift))
	} else {
		rem := new(big.Int)
		v.QuoRem(v, pow10(-shift), rem)
		if rem.Sign() != 0 {
			return 0, fmt.Errorf("цена содержит доли копейки: Int=%s Exp=%d", n.Int, n.Exp)
		}
	}

	if !v.IsInt64() {
		return 0, fmt.Errorf("цена не влезает в int64: Int=%s Exp=%d", n.Int, n.Exp)
	}
	return domain.Money(v.Int64()), nil
}

func pow10(n int32) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}
