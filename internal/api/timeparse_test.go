package api

import "testing"

func TestParseDayBound(t *testing.T) {
	from, err := parseDayBound("2024-06-15", false)
	if err != nil {
		t.Fatal(err)
	}
	if from.Format("2006-01-02T15:04:05Z") != "2024-06-15T00:00:00Z" {
		t.Fatalf("from=%v", from)
	}
	to, err := parseDayBound("2024-06-15", true)
	if err != nil {
		t.Fatal(err)
	}
	if to.Format("2006-01-02T15:04:05Z") != "2024-06-15T23:59:59Z" {
		t.Fatalf("to=%v", to)
	}
}
