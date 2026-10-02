// Command yggdrasil-ratings runs the community ratings service, or writes
// a public aggregate snapshot.
//
//	yggdrasil-ratings serve
//	yggdrasil-ratings snapshot -out ratings.json
//
// Configuration is by environment:
//
//	RATINGS_ADDR         listen address (default :8080)
//	RATINGS_DB           SQLite file (default /data/ratings.db)
//	RATINGS_SECRET       32+ random bytes, hex or text; keys client hashes and batch tags (required; keep it)
//	RATINGS_ADMIN_TOKEN  bearer token for /v1/admin (optional)
//	RATINGS_TRUST_PROXY  true when behind a reverse proxy that sets X-Forwarded-For
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yeixio/yggdrasil-ratings/internal/api"
	"github.com/yeixio/yggdrasil-ratings/internal/store"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yggdrasil-ratings serve | snapshot -out FILE")
		os.Exit(2)
	}
	secret := os.Getenv("RATINGS_SECRET")
	if len(secret) < 32 {
		log.Error("RATINGS_SECRET must be at least 32 characters of random data, and stay the same")
		os.Exit(2)
	}
	st, err := store.Open(env("RATINGS_DB", "/data/ratings.db"), []byte(secret))
	if err != nil {
		log.Error("open database", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	srv := &api.Server{Store: st, Secret: []byte(secret), AdminToken: os.Getenv("RATINGS_ADMIN_TOKEN"),
		TrustProxy: os.Getenv("RATINGS_TRUST_PROXY") == "true", Log: log}

	switch os.Args[1] {
	case "serve":
		hs := &http.Server{Addr: env("RATINGS_ADDR", ":8080"), Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = hs.Shutdown(sctx)
		}()
		log.Info("listening", "addr", hs.Addr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			os.Exit(1)
		}
	case "snapshot":
		fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
		out := fs.String("out", "-", "file to write, or - for standard output")
		_ = fs.Parse(os.Args[2:])
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		snap, err := srv.Snapshot(req)
		if err != nil {
			log.Error("snapshot", "err", err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(snap, "", "  ")
		b = append(b, '\n')
		if *out == "-" {
			_, _ = os.Stdout.Write(b)
		} else if err := os.WriteFile(*out, b, 0o644); err != nil {
			log.Error("write", "err", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: yggdrasil-ratings serve | snapshot -out FILE")
		os.Exit(2)
	}
}
