// Command beacon is the Core control plane: UI, API, alerting, ingest, storage,
// the agent registry — and a local agent.
//
// Core runs an agent too, and that is a design constraint rather than a
// convenience (decision D13). It forces exactly one execution implementation to
// exist. If Core executed flows one way and agents another, the two would drift
// within a month. Core's local agent gets no special treatment: same enrolment,
// same capability declaration, same link, just over loopback.
//
// M3 serves the read-only half: the status wall and the device list, over the
// API in internal/core/api. Ingest, alerting and the local agent arrive in M4
// and M5.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qubered/beacon/internal/buildinfo"
	"github.com/qubered/beacon/internal/core/api"
	"github.com/qubered/beacon/internal/core/store"
	"github.com/qubered/beacon/internal/site"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	addr := flag.String("addr", envOr("BEACON_ADDR", ":8080"), "HTTP listen address")
	dsn := flag.String("database-url", os.Getenv("BEACON_DATABASE_URL"), "PostgreSQL connection URL")
	siteID := flag.String("site", os.Getenv("BEACON_SITE_ID"), "the site this Core serves")
	webDir := flag.String("web", envOr("BEACON_WEB_DIR", "web/dist"), "directory of built web assets")
	flag.Parse()

	if *showVersion {
		fmt.Printf("beacon %s\n", buildinfo.String())
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(*addr, *dsn, *siteID, *webDir, log); err != nil {
		log.Error("core exited", "error", err)
		os.Exit(1)
	}
}

func run(addr, dsn, siteID, webDir string, log *slog.Logger) error {
	if dsn == "" {
		return errors.New("--database-url is required (or set BEACON_DATABASE_URL)")
	}
	if siteID == "" {
		// Refusing rather than inventing a site: D30 scopes every query by
		// site, and a Core that guessed one would write rows nothing else
		// could find.
		return errors.New("--site is required (or set BEACON_SITE_ID)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()

	apiServer := &api.Server{Reader: st, Log: log, Site: site.ID(siteID)}

	mux := http.NewServeMux()
	mux.Handle("/api/", apiServer.Routes())
	mux.Handle("/", spaHandler(webDir))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		log.Info("core listening", "addr", addr, "site", siteID)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// spaHandler serves the built web assets, falling back to index.html so a
// client-side route survives a refresh.
//
// A missing build directory is not fatal: an operator running Core against the
// API alone should get a working API and a clear message, not a process that
// refuses to start because a frontend was never built.
func spaHandler(dir string) http.Handler {
	if _, err := os.Stat(dir); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "web assets are not built; run `npm run build` in web/ or set --web", http.StatusNotFound)
		})
	}
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(dir + r.URL.Path); err != nil && r.URL.Path != "/" {
			http.ServeFile(w, r, dir+"/index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
