// Package scheduler 提供輕量的內建排程器 (免外部套件)。
//
// 功能:每小時於指定分鐘 (例如每小時 10 分) 觸發一次任務。
// 之所以選在整點後幾分鐘,是為了確保撈到官方最新的整點數據。
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"taiwan-aqi-proxy/internal/timeutil"
)

// Job 為排程要執行的任務函式。
type Job func(ctx context.Context)

// Scheduler 為每小時觸發的排程器。
type Scheduler struct {
	minute int // 每小時觸發的分鐘數 (0-59)
	log    *slog.Logger
}

// New 建立排程器。
func New(minute int, log *slog.Logger) *Scheduler {
	return &Scheduler{minute: minute, log: log}
}

// Start 以阻塞方式啟動排程迴圈,直到 context 被取消。
//
// 通常以 goroutine 呼叫: go sched.Start(ctx, job)。
func (s *Scheduler) Start(ctx context.Context, job Job) {
	s.log.Info("排程器啟動", "trigger_minute", s.minute)
	for {
		// 以台灣時間計算下次觸發點,使「每小時第 N 分」對齊台灣時鐘,
		// 不受系統時區影響 (容器未設 TZ 時 time.Now() 預設為 UTC)。
		next := nextRun(timeutil.Now(), s.minute)
		wait := time.Until(next)
		s.log.Info("下次排程觸發時間", "at", next.Format("2006-01-02 15:04:05"), "wait", wait.String())

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.log.Info("排程器收到停止訊號,結束")
			return
		case <-timer.C:
			s.log.Info("排程觸發,開始執行任務")
			job(ctx)
		}
	}
}

// nextRun 計算自 from 之後、下一個「該小時 minute 分 00 秒」的時間點。
//
// 若當前時間已過 (或正好等於) 本小時的觸發點,則順延至下一小時。
// 此函式為純函式,方便單元測試。
func nextRun(from time.Time, minute int) time.Time {
	candidate := time.Date(from.Year(), from.Month(), from.Day(),
		from.Hour(), minute, 0, 0, from.Location())
	if !candidate.After(from) {
		candidate = candidate.Add(time.Hour)
	}
	return candidate
}
