package export

import (
	"database/sql"
	"testing"

	"github.com/gkirk/t0x-map/internal/db"
)

func TestNeedsDecimation(t *testing.T) {
	slow := []db.File{{
		ObsIntervalS: sql.NullFloat64{Float64: 30, Valid: true},
		PosIntervalS: sql.NullFloat64{Float64: 30, Valid: true},
	}}
	if needsDecimation(slow, 30) {
		t.Fatal("30s data should not need 30s decimation")
	}
	if needsDecimation(slow, 1) {
		t.Fatal("30s data should not need 1s decimation")
	}
	fast := []db.File{{
		ObsIntervalS: sql.NullFloat64{Float64: 0.2, Valid: true},
		PosIntervalS: sql.NullFloat64{Float64: 1, Valid: true},
	}}
	if !needsDecimation(fast, 1) {
		t.Fatal("0.2s obs should need 1s decimation")
	}
	unknown := []db.File{{}}
	if !needsDecimation(unknown, 1) {
		t.Fatal("unknown rates should decimate")
	}
}

func TestDownloadName(t *testing.T) {
	files := []db.File{{
		Receiver:  "Alpha",
		StartTime: sql.NullString{String: "2024-06-15T12:00:00Z", Valid: true},
		Ext:       "T04",
	}}
	if g := downloadName(files, RateOriginal, ".T04"); g != "Alpha_20240615.T04" {
		t.Fatalf("got %q", g)
	}
	if g := downloadName(files, Rate1s, ".T04"); g != "Alpha_20240615_1s.T04" {
		t.Fatalf("got %q", g)
	}
}
