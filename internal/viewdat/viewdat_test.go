package viewdat

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGPSWeekSowToUTC(t *testing.T) {
	got := GPSWeekSowToUTC(0, 0)
	want := time.Date(1980, 1, 6, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseX29Sample(t *testing.T) {
	sample := `2431,375446.400,0,25,22,96,20,5,4,0, 40.2952690711,-104.9978292462,  1486.9954,   -0.0023,   -0.0007,   -0.0136,0.0000001043,0.0000000000,  1.03,  0.62,  0.81,  1.06,0.0112,0.0103,0.0244,0.0457,1.0000,1,0.4062,0,Nan
2431,375447.000,0,25,22,96,20,5,4,0, 40.2952690824,-104.9978292579,  1486.9971,    0.0019,   -0.0003,    0.0017,0.0000001192,0.0000000000,  1.03,  0.62,  0.81,  1.06,0.0112,0.0103,0.0244,0.0457,1.0000,1,1.0000,0,Nan
2431,375447.600,0,25,22,96,20,5,4,0, 40.2952691179,-104.9978292672,  1487.0010,    0.0045,   -0.0011,   -0.0010,0.0000001490,0.0000000000,  1.03,  0.62,  0.81,  1.06,0.0112,0.0103,0.0244,0.0457,1.0000,1,0.5938,0,Nan
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.x29")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	info := &Info{}
	if err := parseX29File(path, info); err != nil {
		t.Fatal(err)
	}
	if !info.HasTime {
		t.Fatal("expected HasTime")
	}
	if !info.HasPos {
		t.Fatal("expected HasPos")
	}
	start := GPSWeekSowToUTC(2431, 375446.400)
	end := GPSWeekSowToUTC(2431, 375447.600)
	if !info.Start.Equal(start) {
		t.Fatalf("start got %v want %v", info.Start, start)
	}
	if !info.End.Equal(end) {
		t.Fatalf("end got %v want %v", info.End, end)
	}
	if info.Lat < 40.29 || info.Lat > 40.30 {
		t.Fatalf("lat %v", info.Lat)
	}
	if info.Lon > -104.99 || info.Lon < -105.01 {
		t.Fatalf("lon %v", info.Lon)
	}
	if !info.HasPosInterval {
		t.Fatal("expected HasPosInterval")
	}
	// Samples are 0.6 s apart.
	if info.PosIntervalS < 0.55 || info.PosIntervalS > 0.65 {
		t.Fatalf("pos interval %v", info.PosIntervalS)
	}
}

func TestMedianFloat(t *testing.T) {
	v, ok := medianFloat([]float64{1, 0.2, 0.2, 0.2, 5})
	if !ok || v < 0.19 || v > 0.21 {
		t.Fatalf("got %v %v", v, ok)
	}
}

func TestIsT0xCaseSensitive(t *testing.T) {
	cases := map[string]bool{
		"a.T02":     true,
		"a.T04":     true,
		"a.T05":     true,
		"a.t04":     false,
		"a.T04.bak": false,
	}
	for name, want := range cases {
		if got := IsT0x(name); got != want {
			t.Fatalf("IsT0x(%q)=%v want %v", name, got, want)
		}
	}
}
