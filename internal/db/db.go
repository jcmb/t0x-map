package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type File struct {
	ID        int64
	Path      string
	RelPath   string
	GroupName string
	Receiver  string
	Filename  string
	Ext       string
	SizeBytes int64
	MtimeUnix int64
	StartTime sql.NullString
	EndTime   sql.NullString
	Lat       sql.NullFloat64
	Lon       sql.NullFloat64
	IndexedAt string
}

type Meta struct {
	Groups    []string            `json:"groups"`
	Receivers map[string][]string `json:"receivers"`
}

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	d := &DB{sql: sqlDB}
	if err := d.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.sql.Close()
}

func (d *DB) migrate() error {
	_, err := d.sql.Exec(`
CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  rel_path TEXT NOT NULL,
  group_name TEXT NOT NULL,
  receiver TEXT NOT NULL,
  filename TEXT NOT NULL,
  ext TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  mtime_unix INTEGER NOT NULL,
  start_time TEXT,
  end_time TEXT,
  lat REAL,
  lon REAL,
  indexed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_filters ON files(group_name, receiver, start_time, end_time);
CREATE INDEX IF NOT EXISTS idx_files_geo ON files(lat, lon);
`)
	return err
}

func (d *DB) GetByPath(ctx context.Context, path string) (*File, error) {
	row := d.sql.QueryRowContext(ctx, `
SELECT id, path, rel_path, group_name, receiver, filename, ext,
       size_bytes, mtime_unix, start_time, end_time, lat, lon, indexed_at
FROM files WHERE path = ?`, path)
	return scanFile(row)
}

func (d *DB) GetByID(ctx context.Context, id int64) (*File, error) {
	row := d.sql.QueryRowContext(ctx, `
SELECT id, path, rel_path, group_name, receiver, filename, ext,
       size_bytes, mtime_unix, start_time, end_time, lat, lon, indexed_at
FROM files WHERE id = ?`, id)
	return scanFile(row)
}

func (d *DB) Upsert(ctx context.Context, f *File) error {
	if f.IndexedAt == "" {
		f.IndexedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := d.sql.ExecContext(ctx, `
INSERT INTO files (
  path, rel_path, group_name, receiver, filename, ext,
  size_bytes, mtime_unix, start_time, end_time, lat, lon, indexed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
  rel_path=excluded.rel_path,
  group_name=excluded.group_name,
  receiver=excluded.receiver,
  filename=excluded.filename,
  ext=excluded.ext,
  size_bytes=excluded.size_bytes,
  mtime_unix=excluded.mtime_unix,
  start_time=excluded.start_time,
  end_time=excluded.end_time,
  lat=excluded.lat,
  lon=excluded.lon,
  indexed_at=excluded.indexed_at
`, f.Path, f.RelPath, f.GroupName, f.Receiver, f.Filename, f.Ext,
		f.SizeBytes, f.MtimeUnix, f.StartTime, f.EndTime, f.Lat, f.Lon, f.IndexedAt)
	return err
}

func (d *DB) DeleteMissing(ctx context.Context, keepPaths []string) (int64, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if len(keepPaths) == 0 {
		res, err := tx.ExecContext(ctx, `DELETE FROM files`)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		return n, tx.Commit()
	}

	_, err = tx.ExecContext(ctx, `CREATE TEMP TABLE keep_paths (path TEXT PRIMARY KEY)`)
	if err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO keep_paths(path) VALUES (?)`)
	if err != nil {
		return 0, err
	}
	for _, p := range keepPaths {
		if _, err := stmt.ExecContext(ctx, p); err != nil {
			_ = stmt.Close()
			return 0, err
		}
	}
	_ = stmt.Close()

	res, err := tx.ExecContext(ctx, `
DELETE FROM files WHERE path NOT IN (SELECT path FROM keep_paths)`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	_, _ = tx.ExecContext(ctx, `DROP TABLE keep_paths`)
	return n, tx.Commit()
}

type FileFilter struct {
	Group    string
	Receiver string
	From     time.Time // inclusive start of range (UTC)
	To       time.Time // inclusive end of range (UTC); zero = open
	HasPos   bool
}

func (d *DB) ListFiles(ctx context.Context, f FileFilter) ([]File, error) {
	var (
		conds []string
		args  []any
	)
	if f.HasPos {
		conds = append(conds, "lat IS NOT NULL AND lon IS NOT NULL")
	}
	if f.Group != "" {
		conds = append(conds, "group_name = ?")
		args = append(args, f.Group)
	}
	if f.Receiver != "" {
		conds = append(conds, "receiver = ?")
		args = append(args, f.Receiver)
	}
	// Date range: session overlaps [From, To] by UTC calendar day.
	// Compare YYYY-MM-DD prefixes so RFC3339 / " " / Z variants all work.
	if !f.From.IsZero() || !f.To.IsZero() {
		conds = append(conds, "start_time IS NOT NULL AND end_time IS NOT NULL")
	}
	if !f.From.IsZero() {
		conds = append(conds, "substr(end_time, 1, 10) >= ?")
		args = append(args, f.From.UTC().Format("2006-01-02"))
	}
	if !f.To.IsZero() {
		conds = append(conds, "substr(start_time, 1, 10) <= ?")
		args = append(args, f.To.UTC().Format("2006-01-02"))
	}
	q := `
SELECT id, path, rel_path, group_name, receiver, filename, ext,
       size_bytes, mtime_unix, start_time, end_time, lat, lon, indexed_at
FROM files`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY group_name, receiver, start_time, filename"

	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []File
	for rows.Next() {
		file, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *file)
	}
	return out, rows.Err()
}

func (d *DB) Meta(ctx context.Context) (*Meta, error) {
	rows, err := d.sql.QueryContext(ctx, `
SELECT DISTINCT group_name, receiver FROM files
ORDER BY group_name, receiver`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := &Meta{
		Groups:    []string{},
		Receivers: map[string][]string{},
	}
	seenGroup := map[string]bool{}
	for rows.Next() {
		var g, r string
		if err := rows.Scan(&g, &r); err != nil {
			return nil, err
		}
		r = strings.TrimSpace(r)
		if r == "" {
			// No usable receiver — do not surface the group from this row alone.
			continue
		}
		if !seenGroup[g] {
			seenGroup[g] = true
			m.Groups = append(m.Groups, g)
		}
		m.Receivers[g] = append(m.Receivers[g], r)
	}
	return m, rows.Err()
}

func (d *DB) GetByIDs(ctx context.Context, ids []int64) ([]File, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(`
SELECT id, path, rel_path, group_name, receiver, filename, ext,
       size_bytes, mtime_unix, start_time, end_time, lat, lon, indexed_at
FROM files WHERE id IN (%s)`, strings.Join(placeholders, ","))
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// TimeStats summarizes timestamp coverage in the files table.
type TimeStats struct {
	Total       int
	WithTime    int
	WithoutTime int
	MinStart    string
	MaxEnd      string
	MinStartDay string
	MaxEndDay   string
}

func (d *DB) TimeStats(ctx context.Context, group, receiver string) (*TimeStats, error) {
	var (
		conds []string
		args  []any
	)
	if group != "" {
		conds = append(conds, "group_name = ?")
		args = append(args, group)
	}
	if receiver != "" {
		conds = append(conds, "receiver = ?")
		args = append(args, receiver)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var (
		total, withTime, withoutTime int
		minStart, maxEnd             sql.NullString
	)
	err := d.sql.QueryRowContext(ctx, `
SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN start_time IS NOT NULL AND end_time IS NOT NULL THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN start_time IS NULL OR end_time IS NULL THEN 1 ELSE 0 END), 0),
  MIN(start_time),
  MAX(end_time)
FROM files`+where, args...).Scan(&total, &withTime, &withoutTime, &minStart, &maxEnd)
	if err != nil {
		return nil, err
	}
	st := &TimeStats{
		Total:       total,
		WithTime:    withTime,
		WithoutTime: withoutTime,
	}
	if minStart.Valid {
		st.MinStart = minStart.String
		if len(minStart.String) >= 10 {
			st.MinStartDay = minStart.String[:10]
		}
	}
	if maxEnd.Valid {
		st.MaxEnd = maxEnd.String
		if len(maxEnd.String) >= 10 {
			st.MaxEndDay = maxEnd.String[:10]
		}
	}
	return st, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanFile(row scanner) (*File, error) {
	var f File
	err := row.Scan(
		&f.ID, &f.Path, &f.RelPath, &f.GroupName, &f.Receiver, &f.Filename, &f.Ext,
		&f.SizeBytes, &f.MtimeUnix, &f.StartTime, &f.EndTime, &f.Lat, &f.Lon, &f.IndexedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}
