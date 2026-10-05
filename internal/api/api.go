package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gkirk/t0x-map/internal/config"
	"github.com/gkirk/t0x-map/internal/db"
	"github.com/gkirk/t0x-map/internal/version"
)

const (
	maxZipBodyBytes = 1 << 20 // 1 MiB JSON body
	maxZipIDs       = 5000
)

type Server struct {
	cfg   *config.Config
	store *db.DB
	web   fs.FS
	mux   *http.ServeMux
}

func New(cfg *config.Config, store *db.DB, webFS fs.FS) *Server {
	s := &Server{cfg: cfg, store: store, web: webFS, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return compressHandler(s.mux)
}

func (s *Server) routes() {
	base := s.cfg.BasePath
	s.mux.HandleFunc(base+"/api/meta", s.handleMeta)
	s.mux.HandleFunc(base+"/api/files", s.handleFiles)
	s.mux.HandleFunc(base+"/api/files/", s.handleFileByID)
	s.mux.HandleFunc(base+"/api/download", s.handleDownloadZip)
	s.mux.HandleFunc(base+"/api/download/", s.handleDownloadOne)
	s.mux.Handle(base+"/", s.staticHandler())
}

func (s *Server) staticHandler() http.Handler {
	root, err := fs.Sub(s.web, ".")
	if err != nil {
		root = s.web
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := r.URL.Path
		if s.cfg.BasePath != "" {
			p = strings.TrimPrefix(p, s.cfg.BasePath)
		}
		// Do not use http.FileServer: with an empty proxied path it 301s to
		// Location: ./ which loops when Apache fronts the app at /t0x/.
		if p == "" || p == "/" || p == "." || strings.HasSuffix(p, "/") {
			s.serveFile(w, r, root, "index.html")
			return
		}
		name := path.Clean("/" + strings.TrimPrefix(p, "/"))
		name = strings.TrimPrefix(name, "/")
		if name == "" || name == "." || strings.Contains(name, "..") {
			s.serveFile(w, r, root, "index.html")
			return
		}
		s.serveFile(w, r, root, name)
	})
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, root fs.FS, name string) {
	data, err := fs.ReadFile(root, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ver := strings.TrimSpace(version.Version)
	switch {
	case name == "index.html":
		data = bytes.ReplaceAll(data, []byte("__VERSION__"), []byte(ver))
		// Always revalidate HTML so clients pick up new asset query strings.
		w.Header().Set("Cache-Control", "no-cache")
	case strings.HasSuffix(name, ".js"), strings.HasSuffix(name, ".css"):
		// Query string (?v=) from index.html busts cache on each release.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	meta, err := s.store.Meta(r.Context())
	if err != nil {
		writeServerError(w, "meta", err)
		return
	}
	writeJSON(w, meta)
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	filter := db.FileFilter{
		Group:    q.Get("group"),
		Receiver: q.Get("receiver"),
		HasPos:   false, // count all matches; map uses subset with coordinates
	}
	if v := q.Get("from"); v != "" {
		t, err := parseDayBound(v, false)
		if err != nil {
			http.Error(w, "invalid from", http.StatusBadRequest)
			return
		}
		filter.From = t
	}
	if v := q.Get("to"); v != "" {
		t, err := parseDayBound(v, true)
		if err != nil {
			http.Error(w, "invalid to", http.StatusBadRequest)
			return
		}
		filter.To = t
	}
	files, err := s.store.ListFiles(r.Context(), filter)
	if err != nil {
		writeServerError(w, "files", err)
		return
	}
	writeJSON(w, toFilesResponse(files, filter))
}

func (s *Server) handleFileByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := parseTrailingID(r.URL.Path, "/api/files/")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	f, err := s.store.GetByID(r.Context(), id)
	if err != nil {
		writeServerError(w, "file", err)
		return
	}
	if f == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, fileJSON(f))
}

func (s *Server) handleDownloadOne(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := parseTrailingID(r.URL.Path, "/api/download/")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	f, err := s.store.GetByID(r.Context(), id)
	if err != nil {
		writeServerError(w, "download", err)
		return
	}
	if f == nil {
		http.NotFound(w, r)
		return
	}
	if !underRoot(s.cfg.DataRoot, f.Path) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		http.Error(w, "file missing", http.StatusNotFound)
		return
	}
	defer fh.Close()
	name := contentDispositionFilename(f.Filename)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	http.ServeContent(w, r, name, time.Unix(f.MtimeUnix, 0), fh)
}

type zipReq struct {
	IDs []int64 `json:"ids"`
}

func (s *Server) handleDownloadZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxZipBodyBytes)
	var req zipReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if len(req.IDs) == 0 {
		http.Error(w, "ids required", http.StatusBadRequest)
		return
	}
	if len(req.IDs) > maxZipIDs {
		http.Error(w, "too many ids", http.StatusBadRequest)
		return
	}
	files, err := s.store.GetByIDs(r.Context(), req.IDs)
	if err != nil {
		writeServerError(w, "download zip", err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="t0x-files.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, f := range files {
		if !underRoot(s.cfg.DataRoot, f.Path) {
			continue
		}
		if err := addZipFile(zw, f.RelPath, f.Path); err != nil {
			continue
		}
	}
}

func addZipFile(zw *zip.Writer, name, diskPath string) error {
	entry, err := safeZipEntryName(name)
	if err != nil {
		return err
	}
	src, err := os.Open(diskPath)
	if err != nil {
		return err
	}
	defer src.Close()
	w, err := zw.Create(entry)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}

// safeZipEntryName rejects absolute paths and ".." segments (zip-slip).
func safeZipEntryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" {
		return "", fmt.Errorf("empty zip name")
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe zip name")
	}
	var parts []string
	for _, p := range strings.Split(name, "/") {
		switch p {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("unsafe zip name")
		default:
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("empty zip name")
	}
	return strings.Join(parts, "/"), nil
}

// contentDispositionFilename strips CR/LF/quotes and path separators from a download name.
func contentDispositionFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "download"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\'' || r == '\r' || r == '\n' || r == '\x00':
			b.WriteByte('_')
		case unicode.IsControl(r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "download"
	}
	return out
}

func writeServerError(w http.ResponseWriter, op string, err error) {
	log.Printf("%s: %v", op, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func parseTrailingID(urlPath, marker string) (int64, error) {
	i := strings.LastIndex(urlPath, marker)
	if i < 0 {
		return 0, fmt.Errorf("no id")
	}
	idStr := strings.Trim(urlPath[i+len(marker):], "/")
	return strconv.ParseInt(idStr, 10, 64)
}

func underRoot(root, p string) bool {
	absRoot, err1 := filepath.Abs(root)
	absPath, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	// Compact JSON — indented payloads roughly double transfer size.
	_ = enc.Encode(v)
}

type geoJSON struct {
	Type     string           `json:"type"`
	Features []geoJSONFeature `json:"features"`
	Files    []map[string]any `json:"files"`
	Meta     filesMeta        `json:"meta"`
}

type filesMeta struct {
	Total      int    `json:"total"`
	OnMap      int    `json:"on_map"`
	WithoutPos int    `json:"without_position"`
	WithTime   int    `json:"with_time"`
	WithoutTime int   `json:"without_time"`
	MinStart   string `json:"min_start,omitempty"`
	MaxEnd     string `json:"max_end,omitempty"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	Group      string `json:"group,omitempty"`
	Receiver   string `json:"receiver,omitempty"`
}

type geoJSONFeature struct {
	Type       string         `json:"type"`
	Geometry   geoJSONGeom    `json:"geometry"`
	Properties map[string]any `json:"properties"`
}

type geoJSONGeom struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"`
}

func toFilesResponse(files []db.File, filter db.FileFilter) geoJSON {
	out := geoJSON{
		Type:     "FeatureCollection",
		Features: []geoJSONFeature{},
		Files:    make([]map[string]any, 0, len(files)),
		Meta: filesMeta{
			Total:    len(files),
			Group:    filter.Group,
			Receiver: filter.Receiver,
		},
	}
	if !filter.From.IsZero() {
		out.Meta.From = filter.From.UTC().Format("2006-01-02")
	}
	if !filter.To.IsZero() {
		out.Meta.To = filter.To.UTC().Format("2006-01-02")
	}
	for i := range files {
		f := &files[i]
		props := listFileJSON(f)
		out.Files = append(out.Files, props)
		if f.StartTime.Valid && f.EndTime.Valid {
			out.Meta.WithTime++
			if out.Meta.MinStart == "" || f.StartTime.String < out.Meta.MinStart {
				out.Meta.MinStart = f.StartTime.String
			}
			if out.Meta.MaxEnd == "" || f.EndTime.String > out.Meta.MaxEnd {
				out.Meta.MaxEnd = f.EndTime.String
			}
		} else {
			out.Meta.WithoutTime++
		}
		if !f.Lat.Valid || !f.Lon.Valid {
			out.Meta.WithoutPos++
			continue
		}
		out.Meta.OnMap++
		// Features carry only id + coordinates; UI looks up the rest from files.
		out.Features = append(out.Features, geoJSONFeature{
			Type: "Feature",
			Geometry: geoJSONGeom{
				Type:        "Point",
				Coordinates: []float64{f.Lon.Float64, f.Lat.Float64},
			},
			Properties: map[string]any{"id": f.ID},
		})
	}
	return out
}

// listFileJSON is the compact row used in /api/files listings (no absolute path).
func listFileJSON(f *db.File) map[string]any {
	m := map[string]any{
		"id":         f.ID,
		"group":      f.GroupName,
		"receiver":   f.Receiver,
		"filename":   f.Filename,
		"ext":        f.Ext,
		"size_bytes": f.SizeBytes,
	}
	if f.StartTime.Valid {
		m["start_time"] = f.StartTime.String
	}
	if f.EndTime.Valid {
		m["end_time"] = f.EndTime.String
	}
	if f.Lat.Valid {
		m["lat"] = f.Lat.Float64
	}
	if f.Lon.Valid {
		m["lon"] = f.Lon.Float64
	}
	return m
}

func fileJSON(f *db.File) map[string]any {
	m := listFileJSON(f)
	m["path"] = f.Path
	m["rel_path"] = f.RelPath
	m["mtime_unix"] = f.MtimeUnix
	m["indexed_at"] = f.IndexedAt
	return m
}
