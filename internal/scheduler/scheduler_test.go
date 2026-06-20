package scheduler

import (
	"testing"
	"time"
)

func TestNextRun(t *testing.T) {
	loc := time.UTC

	cases := []struct {
		name   string
		from   time.Time
		minute int
		want   time.Time
	}{
		{
			name:   "尚未到觸發分鐘,排在本小時",
			from:   time.Date(2026, 6, 20, 11, 5, 0, 0, loc),
			minute: 10,
			want:   time.Date(2026, 6, 20, 11, 10, 0, 0, loc),
		},
		{
			name:   "已過觸發分鐘,順延至下一小時",
			from:   time.Date(2026, 6, 20, 11, 15, 0, 0, loc),
			minute: 10,
			want:   time.Date(2026, 6, 20, 12, 10, 0, 0, loc),
		},
		{
			name:   "正好等於觸發分鐘,順延 (避免重複觸發)",
			from:   time.Date(2026, 6, 20, 11, 10, 0, 0, loc),
			minute: 10,
			want:   time.Date(2026, 6, 20, 12, 10, 0, 0, loc),
		},
		{
			name:   "跨日:23 時後順延至隔日 00 時",
			from:   time.Date(2026, 6, 20, 23, 30, 0, 0, loc),
			minute: 10,
			want:   time.Date(2026, 6, 21, 0, 10, 0, 0, loc),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nextRun(c.from, c.minute); !got.Equal(c.want) {
				t.Errorf("nextRun(%v, %d) = %v, want %v", c.from, c.minute, got, c.want)
			}
		})
	}
}
