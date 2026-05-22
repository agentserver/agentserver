package db

import (
	"testing"
	"time"
)

func TestParseZonedToUTC(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		tz      string
		wantUTC string
		wantErr bool
	}{
		{"explicit UTC Z", "2026-05-22T09:00:00Z", "Asia/Shanghai", "2026-05-22T09:00:00Z", false},
		{"explicit +00:00", "2026-05-22T09:00:00+00:00", "Asia/Shanghai", "2026-05-22T09:00:00Z", false},
		{"naive local CST -> UTC", "2026-05-22T09:00:00", "Asia/Shanghai", "2026-05-22T01:00:00Z", false},
		{"naive local UTC unchanged", "2026-05-22T09:00:00", "UTC", "2026-05-22T09:00:00Z", false},
		{"bad string", "not-a-date", "UTC", "", true},
		{"bad tz", "2026-05-22T09:00:00", "Not/AZone", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseZonedToUTC(c.in, c.tz)
			if c.wantErr {
				if err == nil { t.Fatalf("want error, got %v", got) }
				return
			}
			if err != nil { t.Fatalf("unexpected err: %v", err) }
			want, _ := time.Parse(time.RFC3339, c.wantUTC)
			if !got.Equal(want) {
				t.Fatalf("got %s want %s", got.Format(time.RFC3339), c.wantUTC)
			}
		})
	}
}
