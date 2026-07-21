package monitor

import (
	"context"
	"time"
)

// Run гоняет мониторинг до отмены контекста.
//
// Два требования к циклу:
//  1. Первый прогон — сразу при старте, до входа в тикер. Иначе первые CHECK_INTERVAL
//     минут парсер молчит, и демо выглядит сломанным.
//  2. Цикл слушает и тикер, и ctx.Done() (ctx от signal.NotifyContext), чтобы по сигналу
//     завершения горутина выходила без утечки, а не досыпала интервал.
func (m *Monitor) Run(ctx context.Context, interval time.Duration) error {
	if err := m.RunOnce(ctx); err != nil {
		// Ошибку первого прогона возвращаем: обычно это недоступная БД на старте,
		// и продолжать цикл поверх мёртвой базы смысла нет.
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.log.Info("получен сигнал завершения, мониторинг остановлен")
			return nil
		case <-ticker.C:
			// Ошибку периодического прогона не возвращаем: разовый сбой (сеть, БД
			// моргнула) не должен ронять демона — следующий тик попробует снова.
			if err := m.RunOnce(ctx); err != nil {
				m.log.Error("прогон завершился с ошибкой", "error", err)
			}
		}
	}
}
