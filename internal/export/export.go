package export

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gkirk/t0x-map/internal/combine"
	"github.com/gkirk/t0x-map/internal/db"
)

type Rate string

const (
	RateOriginal Rate = "original"
	Rate1s       Rate = "1s"
	Rate30s      Rate = "30s"
)

type Options struct {
	PythonPath  string
	T0x2t0xPath string
	DataRoot    string
}

type Result struct {
	Path     string // absolute path to the file to stream
	Filename string // download basename
	Cleanup  func()
}

// Build combines (when needed) and optionally decimates files into one T0x output.
func Build(ctx context.Context, files []db.File, rate Rate, opt Options) (*Result, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no files")
	}
	switch rate {
	case RateOriginal, Rate1s, Rate30s:
	default:
		return nil, fmt.Errorf("invalid rate %q", rate)
	}
	if opt.PythonPath == "" {
		opt.PythonPath = "python3"
	}
	if opt.T0x2t0xPath == "" {
		opt.T0x2t0xPath = "/usr/local/bin/t0x2t0x"
	}

	work, err := os.MkdirTemp("", "t0x-export-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(work) }

	// Stage inputs with safe basenames into work dir.
	var staged []string
	for i, f := range files {
		name := filepath.Base(f.Filename)
		if name == "" || name == "." {
			name = fmt.Sprintf("file%d.%s", i, f.Ext)
		}
		dst := filepath.Join(work, name)
		if err := linkOrCopy(f.Path, dst); err != nil {
			cleanup()
			return nil, fmt.Errorf("stage %s: %w", f.Filename, err)
		}
		staged = append(staged, dst)
	}

	combined, err := combineFiles(ctx, opt.PythonPath, work, staged)
	if err != nil {
		cleanup()
		return nil, err
	}

	outPath := combined
	decN := 0
	switch rate {
	case Rate1s:
		decN = 1
	case Rate30s:
		decN = 30
	}
	if decN > 0 && needsDecimation(files, float64(decN)) {
		decPath := filepath.Join(work, decimatedName(filepath.Base(combined), decN))
		if err := runT0x2t0x(ctx, opt.T0x2t0xPath, decN, combined, decPath); err != nil {
			cleanup()
			return nil, err
		}
		outPath = decPath
	}

	dlName := downloadName(files, rate, filepath.Ext(outPath))
	return &Result{
		Path:     outPath,
		Filename: dlName,
		Cleanup:  cleanup,
	}, nil
}

func needsDecimation(files []db.File, target float64) bool {
	for _, f := range files {
		if f.ObsIntervalS.Valid && f.ObsIntervalS.Float64 > 0 && f.ObsIntervalS.Float64 < target-1e-9 {
			return true
		}
		if f.PosIntervalS.Valid && f.PosIntervalS.Float64 > 0 && f.PosIntervalS.Float64 < target-1e-9 {
			return true
		}
	}
	// Unknown rates: still run decimation so the user gets the requested interval.
	for _, f := range files {
		if !f.ObsIntervalS.Valid && !f.PosIntervalS.Valid {
			return true
		}
	}
	return false
}

func combineFiles(ctx context.Context, python, work string, staged []string) (string, error) {
	if len(staged) == 1 {
		return staged[0], nil
	}
	script := filepath.Join(work, "T0x_Combine.py")
	if err := os.WriteFile(script, combine.Script, 0o755); err != nil {
		return "", err
	}
	args := []string{script, "--Clobber"}
	args = append(args, staged...)
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = work
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("T0x_Combine.py: %w (%s)", err, truncate(string(out), 600))
	}
	// Pick the newest non-script T0x in the work dir that isn't an input basename-only leftover.
	combined, err := findCombinedOutput(work, staged)
	if err != nil {
		return "", fmt.Errorf("combine output: %w (%s)", err, truncate(string(out), 400))
	}
	return combined, nil
}

func findCombinedOutput(work string, staged []string) (string, error) {
	stagedSet := map[string]struct{}{}
	for _, p := range staged {
		stagedSet[filepath.Base(p)] = struct{}{}
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		return "", err
	}
	var best string
	var bestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "T0x_Combine.py" {
			continue
		}
		ext := strings.ToUpper(filepath.Ext(name))
		if ext != ".T02" && ext != ".T04" && ext != ".T05" {
			continue
		}
		// Prefer files that are not the original staged inputs (combine writes group_key.ext).
		if _, isStaged := stagedSet[name]; isStaged {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestMod) {
			best = filepath.Join(work, name)
			bestMod = info.ModTime()
		}
	}
	if best == "" {
		// Single-group rename may overwrite; fall back to first staged if combine skipped.
		if len(staged) == 1 {
			return staged[0], nil
		}
		return "", fmt.Errorf("no combined T0x produced")
	}
	return best, nil
}

func runT0x2t0x(ctx context.Context, bin string, n int, inPath, outPath string) error {
	cmd := exec.CommandContext(ctx, bin,
		fmt.Sprintf("-obs_dec=%d", n),
		fmt.Sprintf("-pos_dec=%d", n),
		inPath,
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("t0x2t0x: %w (%s)", err, truncate(string(out), 600))
	}
	return nil
}

func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = out.ReadFrom(in)
	return err
}

func downloadName(files []db.File, rate Rate, ext string) string {
	if ext == "" {
		ext = ".T04"
	}
	recv := "t0x"
	if len(files) > 0 && strings.TrimSpace(files[0].Receiver) != "" {
		recv = files[0].Receiver
	}
	day := ""
	for _, f := range files {
		if f.StartTime.Valid && len(f.StartTime.String) >= 10 {
			day = strings.ReplaceAll(f.StartTime.String[:10], "-", "")
			break
		}
	}
	base := recv
	if day != "" {
		base = recv + "_" + day
	}
	switch rate {
	case Rate1s:
		base += "_1s"
	case Rate30s:
		base += "_30s"
	}
	return base + strings.ToUpper(ext)
}

func decimatedName(combinedBase string, n int) string {
	ext := filepath.Ext(combinedBase)
	stem := strings.TrimSuffix(combinedBase, ext)
	return fmt.Sprintf("%s_%ds%s", stem, n, ext)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
