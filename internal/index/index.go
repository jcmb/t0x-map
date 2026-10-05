package index

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gkirk/t0x-map/internal/config"
	"github.com/gkirk/t0x-map/internal/db"
	"github.com/gkirk/t0x-map/internal/viewdat"
)

type Indexer struct {
	cfg     *config.Config
	store   *db.DB
	viewdat *viewdat.Client
	log     *log.Logger
}

func New(cfg *config.Config, store *db.DB, logger *log.Logger) *Indexer {
	if logger == nil {
		logger = log.Default()
	}
	return &Indexer{
		cfg:     cfg,
		store:   store,
		viewdat: viewdat.New(cfg.ViewdatPath),
		log:     logger,
	}
}

type Stats struct {
	Seen    int
	Indexed int
	Skipped int
	Deleted int64
	Errors  int
}

type Options struct {
	// Progress logs [n/total] lines as each file is processed.
	Progress bool
}

func (idx *Indexer) Full(ctx context.Context, opts Options) (*Stats, error) {
	st := &Stats{}
	paths, err := idx.listFiles()
	if err != nil {
		return st, err
	}
	total := len(paths)
	if opts.Progress {
		idx.log.Printf("found %d T0x file(s) under %s", total, idx.cfg.DataRoot)
	}

	keep := make([]string, 0, total)
	for i, path := range paths {
		keep = append(keep, path)
		st.Seen++
		action, err := idx.indexPath(ctx, path, st)
		if err != nil {
			idx.log.Printf("index %s: %v", path, err)
			st.Errors++
			action = "error"
		}
		if opts.Progress {
			rel := path
			if r, err := filepath.Rel(idx.cfg.DataRoot, path); err == nil {
				rel = filepath.ToSlash(r)
			}
			idx.log.Printf("[%d/%d] %s %s", i+1, total, action, rel)
		}
	}

	n, err := idx.store.DeleteMissing(ctx, keep)
	if err != nil {
		return st, fmt.Errorf("delete missing: %w", err)
	}
	st.Deleted = n
	if opts.Progress && n > 0 {
		idx.log.Printf("removed %d stale database row(s)", n)
	}
	return st, nil
}

func (idx *Indexer) listFiles() ([]string, error) {
	var paths []string
	for _, group := range idx.cfg.Groups {
		groupDir := filepath.Join(idx.cfg.DataRoot, group)
		info, err := os.Stat(groupDir)
		if err != nil {
			if os.IsNotExist(err) {
				idx.log.Printf("group missing, skipping: %s", groupDir)
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			idx.log.Printf("group is not a directory, skipping: %s", groupDir)
			continue
		}

		receivers, err := os.ReadDir(groupDir)
		if err != nil {
			return nil, err
		}
		for _, rec := range receivers {
			if !rec.IsDir() || strings.HasPrefix(rec.Name(), ".") {
				continue
			}
			recDir := filepath.Join(groupDir, rec.Name())
			entries, err := os.ReadDir(recDir)
			if err != nil {
				idx.log.Printf("read receiver %s: %v", recDir, err)
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !viewdat.IsT0x(e.Name()) {
					continue
				}
				paths = append(paths, filepath.Join(recDir, e.Name()))
			}
		}
	}
	return paths, nil
}

func (idx *Indexer) One(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if !viewdat.IsT0x(filepath.Base(abs)) {
		return fmt.Errorf("not a .T02/.T04/.T05 file: %s", abs)
	}
	st := &Stats{}
	_, err = idx.indexPath(ctx, abs, st)
	return err
}

// indexPath returns action: "indexed", "skip", or "" on hard failure before action.
func (idx *Indexer) indexPath(ctx context.Context, path string, st *Stats) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return "", fmt.Errorf("path is a directory: %s", path)
	}

	existing, err := idx.store.GetByPath(ctx, path)
	if err != nil {
		return "", err
	}
	mtime := fi.ModTime().Unix()
	// Re-extract when timestamps are missing even if size/mtime unchanged.
	if existing != nil && existing.SizeBytes == fi.Size() && existing.MtimeUnix == mtime &&
		existing.StartTime.Valid && existing.EndTime.Valid {
		st.Skipped++
		return "skip", nil
	}

	group, receiver, filename, err := viewdat.RelParts(idx.cfg.DataRoot, path)
	if err != nil {
		return "", err
	}
	if !groupAllowed(idx.cfg.Groups, group) {
		return "", fmt.Errorf("group %q not in config", group)
	}

	rel := filepath.ToSlash(filepath.Join(group, receiver, filename))
	rec := &db.File{
		Path:      path,
		RelPath:   rel,
		GroupName: group,
		Receiver:  receiver,
		Filename:  filename,
		Ext:       viewdat.ExtOf(filename),
		SizeBytes: fi.Size(),
		MtimeUnix: mtime,
		IndexedAt: time.Now().UTC().Format(time.RFC3339),
	}

	info, err := idx.viewdat.Extract(ctx, path)
	if err != nil {
		idx.log.Printf("viewdat %s: %v (storing without metadata)", path, err)
	} else {
		if info.HasTime {
			rec.StartTime = sql.NullString{String: info.Start.UTC().Format(time.RFC3339), Valid: true}
			rec.EndTime = sql.NullString{String: info.End.UTC().Format(time.RFC3339), Valid: true}
		}
		if info.HasPos {
			rec.Lat = sql.NullFloat64{Float64: info.Lat, Valid: true}
			rec.Lon = sql.NullFloat64{Float64: info.Lon, Valid: true}
		}
	}

	if err := idx.store.Upsert(ctx, rec); err != nil {
		return "", err
	}
	st.Indexed++
	return "indexed", nil
}

func groupAllowed(groups []string, name string) bool {
	for _, g := range groups {
		if g == name {
			return true
		}
	}
	return false
}

// WalkConfigured is unused helper kept for tests.
func WalkConfigured(root string, groups []string, fn func(path string, d fs.DirEntry) error) error {
	for _, g := range groups {
		base := filepath.Join(root, g)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !viewdat.IsT0x(d.Name()) {
				return nil
			}
			return fn(path, d)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
