package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gkirk/t0x-map/internal/api"
	"github.com/gkirk/t0x-map/internal/config"
	"github.com/gkirk/t0x-map/internal/db"
	"github.com/gkirk/t0x-map/internal/index"
	"github.com/gkirk/t0x-map/internal/version"
	"github.com/gkirk/t0x-map/internal/webui"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("t0x-map: ")

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "index":
		if err := runIndex(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "files":
		if err := runFiles(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "version", "-version", "--version":
		fmt.Println(strings.TrimSpace(version.Version))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `t0x-map %s — map browser for T02/T04/T05 files

Usage:
  t0x-map serve -config /etc/t0x-map/config.yaml
  t0x-map index -config /etc/t0x-map/config.yaml [-progress]
  t0x-map index -config /etc/t0x-map/config.yaml -file /path/to/file.T04
  t0x-map files -config /etc/t0x-map/config.yaml [-group G] [-receiver R] [-limit N]
  t0x-map files -config /etc/t0x-map/config.yaml -stats
  t0x-map version
`, strings.TrimSpace(version.Version))
}

func runServe(args []string) error {
	fsFlags := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fsFlags.String("config", "/etc/t0x-map/config.yaml", "path to config.yaml")
	_ = fsFlags.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	staticFS, err := fs.Sub(webui.Static, "static")
	if err != nil {
		return err
	}
	srv := api.New(cfg, store, staticFS)
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on http://%s (base_path=%q)", cfg.Listen, cfg.BasePath)
		errCh <- httpSrv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		log.Printf("shutdown (%v)", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(ctx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func runIndex(args []string) error {
	fsFlags := flag.NewFlagSet("index", flag.ExitOnError)
	cfgPath := fsFlags.String("config", "/etc/t0x-map/config.yaml", "path to config.yaml")
	oneFile := fsFlags.String("file", "", "index a single T0x file (post-download hook)")
	progress := fsFlags.Bool("progress", false, "log [n/total] progress for each file")
	_ = fsFlags.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	idx := index.New(cfg, store, log.Default())
	ctx := context.Background()

	if *oneFile != "" {
		if err := idx.One(ctx, *oneFile); err != nil {
			return err
		}
		log.Printf("indexed %s", *oneFile)
		return nil
	}

	st, err := idx.Full(ctx, index.Options{Progress: *progress})
	if err != nil {
		return err
	}
	log.Printf("index complete: seen=%d indexed=%d skipped=%d deleted=%d errors=%d",
		st.Seen, st.Indexed, st.Skipped, st.Deleted, st.Errors)
	return nil
}

func runFiles(args []string) error {
	fsFlags := flag.NewFlagSet("files", flag.ExitOnError)
	cfgPath := fsFlags.String("config", "/etc/t0x-map/config.yaml", "path to config.yaml")
	group := fsFlags.String("group", "", "filter by group")
	receiver := fsFlags.String("receiver", "", "filter by receiver")
	limit := fsFlags.Int("limit", 50, "max rows to print (0 = all)")
	statsOnly := fsFlags.Bool("stats", false, "print timestamp coverage summary only")
	_ = fsFlags.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx := context.Background()

	ts, err := store.TimeStats(ctx, *group, *receiver)
	if err != nil {
		return err
	}
	fmt.Printf("db: %s\n", cfg.DBPath)
	if *group != "" || *receiver != "" {
		fmt.Printf("filter: group=%q receiver=%q\n", *group, *receiver)
	}
	fmt.Printf("rows: total=%d with_time=%d without_time=%d\n",
		ts.Total, ts.WithTime, ts.WithoutTime)
	if ts.MinStart != "" || ts.MaxEnd != "" {
		fmt.Printf("start_time range: %s .. %s\n", nullDash(ts.MinStart), nullDash(ts.MaxEnd))
		fmt.Printf("UTC days:         %s .. %s\n", nullDash(ts.MinStartDay), nullDash(ts.MaxEndDay))
	}
	if *statsOnly {
		if ts.WithoutTime > 0 {
			fmt.Println("note: files without start/end time are excluded from date filters")
		}
		return nil
	}

	files, err := store.ListFiles(ctx, db.FileFilter{
		Group:    *group,
		Receiver: *receiver,
	})
	if err != nil {
		return err
	}
	n := len(files)
	if *limit > 0 && n > *limit {
		n = *limit
	}
	fmt.Printf("\n%-6s %-12s %-20s %-36s %-22s %-22s %s\n",
		"id", "group", "receiver", "filename", "start_time", "end_time", "lat,lon")
	for i := 0; i < n; i++ {
		f := files[i]
		start := "NULL"
		end := "NULL"
		if f.StartTime.Valid {
			start = f.StartTime.String
		}
		if f.EndTime.Valid {
			end = f.EndTime.String
		}
		pos := "NULL"
		if f.Lat.Valid && f.Lon.Valid {
			pos = fmt.Sprintf("%.5f,%.5f", f.Lat.Float64, f.Lon.Float64)
		}
		fmt.Printf("%-6d %-12s %-20s %-36s %-22s %-22s %s\n",
			f.ID, trunc(f.GroupName, 12), trunc(f.Receiver, 20), trunc(f.Filename, 36),
			trunc(start, 22), trunc(end, 22), pos)
	}
	if *limit > 0 && len(files) > *limit {
		fmt.Printf("… %d more (raise -limit or use -limit 0)\n", len(files)-*limit)
	}
	if ts.WithoutTime > 0 {
		fmt.Println("\nnote: files with NULL start/end are excluded when From/To date filters are set")
	}
	return nil
}

func nullDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
