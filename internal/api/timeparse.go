package api

import (
	"fmt"
	"strings"
	"time"
)

// parseDayBound parses a filter bound as YYYY-MM-DD (date-only) or RFC3339.
// endOfDay: for "to", use 23:59:59 UTC so the selected day is inclusive.
func parseDayBound(v string, endOfDay bool) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", v, time.UTC); err == nil {
		if endOfDay {
			return t.Add(24*time.Hour - time.Second), nil
		}
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("want YYYY-MM-DD or RFC3339, got %q", v)
}
