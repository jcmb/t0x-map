package viewdat

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Info struct {
	Start          time.Time
	End            time.Time
	Lat            float64
	Lon            float64
	HasPos         bool
	HasTime        bool
	PosIntervalS   float64 // position spacing (seconds); 0 if unknown
	HasPosInterval bool
	ObsIntervalS   float64 // epoch / raw spacing (seconds); 0 if unknown
	HasObsInterval bool
}

type Client struct {
	Bin string
}

func New(bin string) *Client {
	if bin == "" {
		bin = "viewdat"
	}
	return &Client{Bin: bin}
}

// GPS epoch 1980-01-06 00:00:00 UTC (leap seconds ignored, same as GNSS_Plotting).
var gpsEpoch = time.Date(1980, 1, 6, 0, 0, 0, 0, time.UTC)

const secondsPerWeek = 7 * 24 * 3600

func GPSWeekSowToUTC(week int, sow float64) time.Time {
	whole := math.Floor(sow)
	frac := sow - whole
	sec := int64(week)*secondsPerWeek + int64(whole)
	return gpsEpoch.Add(time.Duration(sec)*time.Second + time.Duration(frac*1e9)*time.Nanosecond)
}

func (c *Client) Extract(ctx context.Context, filePath string) (*Info, error) {
	info := &Info{}
	if err := c.parseX29(ctx, filePath, info); err != nil {
		return info, err
	}
	// Epoch (raw) spacing from measurement dump; best-effort.
	if err := c.parseEpochSpacing(ctx, filePath, info); err != nil {
		// Position extract already succeeded; leave obs interval unset.
		_ = err
	}
	return info, nil
}

func (c *Client) parseX29(ctx context.Context, filePath string, info *Info) error {
	tmp, err := os.CreateTemp("", "t0x-map-*.x29")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	// Do not pass --dec: viewdat ignores it for this dump path; we sample in-process.
	cmd := exec.CommandContext(ctx, c.Bin,
		"-d29",
		"--translate_rec35_sub2_to_rec29",
		"-x",
		"-o"+tmpPath,
		filePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("viewdat -d29: %w (%s)", err, truncate(string(out), 400))
	}
	return parseX29File(tmpPath, info)
}

func (c *Client) parseEpochSpacing(ctx context.Context, filePath string, info *Info) error {
	tmp, err := os.CreateTemp("", "t0x-map-*.x27")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.CommandContext(ctx, c.Bin,
		"-d27",
		"--translate_rec35_sub9_to_rec27",
		"-x",
		"-o"+tmpPath,
		filePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("viewdat -d27: %w (%s)", err, truncate(string(out), 400))
	}
	interval, ok := medianEpochSpacing(tmpPath)
	if !ok {
		return fmt.Errorf("no usable measurement epochs")
	}
	info.ObsIntervalS = interval
	info.HasObsInterval = true
	return nil
}

// parseX29File reads viewdat -x CSV:
//
//	field 0 = GPS week, field 1 = seconds of week, fields 10/11 = lat/lon.
func parseX29File(path string, info *Info) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var (
		sumLat, sumLon float64
		nPos           int
		first, last    time.Time
		haveTime       bool
		lineNo         int
		prevSOW        float64
		havePrev       bool
		deltas         []float64
	)

	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 12 {
			continue
		}

		week, errW := strconv.Atoi(strings.TrimSpace(fields[0]))
		sow, errS := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if errW == nil && errS == nil && week > 0 && sow >= 0 && sow < float64(secondsPerWeek)+1 {
			t := GPSWeekSowToUTC(week, sow)
			if !haveTime {
				first = t
				haveTime = true
			}
			last = t
			if havePrev {
				d := sow - prevSOW
				if d < 0 {
					d += float64(secondsPerWeek)
				}
				if d > 1e-6 && d < 3600 {
					deltas = append(deltas, d)
				}
			}
			prevSOW = sow
			havePrev = true
		}

		// Sample positions (~every 60th epoch) to keep large files cheap.
		if (lineNo-1)%60 != 0 && nPos > 0 {
			continue
		}
		la, err1 := strconv.ParseFloat(strings.TrimSpace(fields[10]), 64)
		lo, err2 := strconv.ParseFloat(strings.TrimSpace(fields[11]), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if math.Abs(la) > 90 || math.Abs(lo) > 180 || (la == 0 && lo == 0) {
			continue
		}
		nPos++
		sumLat += la
		sumLon += lo
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !haveTime && nPos == 0 {
		return fmt.Errorf("no usable X29 records")
	}
	if haveTime {
		info.Start = first
		info.End = last
		info.HasTime = true
	}
	if nPos > 0 {
		info.Lat = sumLat / float64(nPos)
		info.Lon = sumLon / float64(nPos)
		info.HasPos = true
	}
	if interval, ok := medianFloat(deltas); ok {
		info.PosIntervalS = interval
		info.HasPosInterval = true
	}
	return nil
}

func medianEpochSpacing(path string) (float64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var (
		prevSOW  float64
		havePrev bool
		deltas   []float64
	)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		week, errW := strconv.Atoi(strings.TrimSpace(fields[0]))
		sow, errS := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if errW != nil || errS != nil || week <= 0 || sow < 0 || sow >= float64(secondsPerWeek)+1 {
			continue
		}
		if havePrev {
			d := sow - prevSOW
			if d < 0 {
				d += float64(secondsPerWeek)
			}
			if d > 1e-6 && d < 3600 {
				deltas = append(deltas, d)
			}
		}
		prevSOW = sow
		havePrev = true
	}
	if err := sc.Err(); err != nil {
		return 0, false
	}
	return medianFloat(deltas)
}

func medianFloat(vals []float64) (float64, bool) {
	if len(vals) == 0 {
		return 0, false
	}
	sort.Float64s(vals)
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return vals[mid], true
	}
	return (vals[mid-1] + vals[mid]) / 2, true
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// RelParts returns group, receiver, filename for path under dataRoot.
func RelParts(dataRoot, absPath string) (group, receiver, filename string, err error) {
	absRoot, err := filepath.Abs(dataRoot)
	if err != nil {
		return "", "", "", err
	}
	absFile, err := filepath.Abs(absPath)
	if err != nil {
		return "", "", "", err
	}
	rel, err := filepath.Rel(absRoot, absFile)
	if err != nil {
		return "", "", "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", "", "", fmt.Errorf("path %s is outside data_root", absPath)
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if len(parts) < 3 {
		return "", "", "", fmt.Errorf("expected Group/Receiver/file under data_root, got %s", rel)
	}
	return parts[0], parts[1], parts[len(parts)-1], nil
}

func IsT0x(name string) bool {
	return strings.HasSuffix(name, ".T02") ||
		strings.HasSuffix(name, ".T04") ||
		strings.HasSuffix(name, ".T05")
}

func ExtOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".T02"):
		return "T02"
	case strings.HasSuffix(name, ".T04"):
		return "T04"
	case strings.HasSuffix(name, ".T05"):
		return "T05"
	default:
		return ""
	}
}
