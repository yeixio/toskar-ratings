// Package api is the community ratings HTTP API (v1).
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/yeixio/toskar-ratings/internal/aggregate"
	"github.com/yeixio/toskar-ratings/internal/schema"
	"github.com/yeixio/toskar-ratings/internal/store"
)

// Server serves the API.
type Server struct {
	Store *store.Store
	// Secret keys batch tags; the store keys client hashes with it too.
	Secret []byte
	// AdminToken guards /v1/admin; empty turns it off.
	AdminToken string
	// TrustProxy reads the client address from X-Forwarded-For, for a
	// service behind a reverse proxy.
	TrustProxy bool
	Log        *slog.Logger
	Now        func() time.Time

	writes *limiter
	reads  *limiter
}

const maxBody = 16 << 10

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	if s.writes == nil {
		s.writes = newLimiter(30, time.Hour)
	}
	if s.reads == nil {
		s.reads = newLimiter(1200, time.Hour)
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/ratings", s.limited(s.writes, s.create))
	mux.HandleFunc("PUT /v1/ratings/{key}", s.limited(s.writes, s.update))
	mux.HandleFunc("DELETE /v1/ratings/{key}", s.limited(s.writes, s.delete))
	mux.HandleFunc("GET /v1/models/{model}/ratings", s.limited(s.reads, s.modelRatings))
	mux.HandleFunc("GET /v1/aggregates", s.limited(s.reads, s.aggregates))
	mux.HandleFunc("GET /v1/admin/batches", s.admin(s.batches))
	mux.HandleFunc("POST /v1/admin/invalidate", s.admin(s.invalidate))
	return mux
}

type apiError struct {
	Error string `json:"error"`
}

// now is the time; Now replaces it in tests.
func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) { writeJSON(w, status, apiError{Error: msg}) }

// clientAddr is the caller's address, used only for rate limits and batch
// tags, and never stored.
func (s *Server) clientAddr(r *http.Request) netip.Addr {
	if s.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if a, err := netip.ParseAddr(first); err == nil {
				return a
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a
}

// prefix is the network a caller is on: its /24, or /48 for IPv6.
func prefix(a netip.Addr) string {
	bits := 24
	if a.Is6() && !a.Is4In6() {
		bits = 48
	}
	p, err := a.Unmap().Prefix(bits)
	if err != nil {
		return "unknown"
	}
	return p.String()
}

// batch tags writes by hour and a keyed hash of the network, so a flood can
// be found and invalidated without storing anyone's address.
func (s *Server) batch(r *http.Request) string {
	m := hmac.New(sha256.New, s.Secret)
	m.Write([]byte("batch:" + prefix(s.clientAddr(r))))
	return s.now().UTC().Format("2006010215") + "-" + hex.EncodeToString(m.Sum(nil))[:12]
}

func (s *Server) limited(l *limiter, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(prefix(s.clientAddr(r))) {
			w.Header().Set("Retry-After", "3600")
			fail(w, http.StatusTooManyRequests, "too many requests; try again later")
			return
		}
		h(w, r)
	}
}

func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.AdminToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.AdminToken)) != 1 {
			fail(w, http.StatusUnauthorized, "admin token required")
			return
		}
		h(w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "the body is not a valid rating: "+err.Error())
		return false
	}
	return true
}

type putResponse struct {
	Key       string `json:"key"`
	Created   bool   `json:"created"`
	UpdatedAt string `json:"updated_at"`
}

// create stores a rating, or updates the client's rating of the same
// configuration.
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var rating schema.Rating
	if !decode(w, r, &rating) {
		return
	}
	if err := rating.Validate(); err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	key, created, err := s.Store.Put(r.Context(), rating, s.batch(r))
	if err != nil {
		s.Log.Error("store rating", "err", err)
		fail(w, http.StatusInternalServerError, "the rating could not be stored")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, putResponse{Key: key, Created: created, UpdatedAt: s.now().UTC().Format(time.RFC3339)})
}

type updateRequest struct {
	ClientID     string               `json:"client_id"`
	Stars        int                  `json:"stars"`
	Tags         []string             `json:"tags,omitempty"`
	Observations *schema.Observations `json:"observations,omitempty"`
	Language     string               `json:"language,omitempty"`
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if !decode(w, r, &req) {
		return
	}
	// Reuse the full validation for the parts an update can change.
	probe := schema.Rating{SchemaVersion: schema.Version, ClientID: req.ClientID, Stars: req.Stars, Tags: req.Tags, Observations: req.Observations, Language: req.Language,
		Model:    schema.Model{ID: "probe", Format: "gguf", Quantization: "Q4"},
		Runtime:  schema.Runtime{Type: "llamacpp", Backend: "cpu"},
		Hardware: schema.Hardware{Platform: "linux", Architecture: "amd64", Vendor: "cpu", Family: "probe", MemoryType: "system", MemoryBucket: "8-16"}}
	if err := probe.Validate(); err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.done(w, s.Store.Update(r.Context(), r.PathValue("key"), req.ClientID, req.Stars, req.Tags, req.Observations, req.Language, s.batch(r)), http.StatusOK)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	s.done(w, s.Store.Delete(r.Context(), r.PathValue("key"), r.Header.Get("X-Ratings-Client")), http.StatusNoContent)
}

func (s *Server) done(w http.ResponseWriter, err error, ok int) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrForbidden):
		fail(w, http.StatusForbidden, err.Error())
	case err != nil:
		s.Log.Error("change rating", "err", err)
		fail(w, http.StatusInternalServerError, "the rating could not be changed")
	case ok == http.StatusNoContent:
		w.WriteHeader(ok)
	default:
		writeJSON(w, ok, map[string]string{"status": "updated"})
	}
}

type modelResponse struct {
	SchemaVersion int               `json:"schema_version"`
	Model         string            `json:"model"`
	Format        string            `json:"format"`
	Quantization  string            `json:"quantization"`
	Runtime       string            `json:"runtime"`
	Backend       string            `json:"backend"`
	Tiers         []aggregate.Stats `json:"tiers"`
}

// modelRatings answers "how do people with hardware like mine rate this?"
// for one configuration, narrowest tier first.
func (s *Server) modelRatings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c := store.Config{Model: r.PathValue("model"), Format: q.Get("format"), Quantization: q.Get("quantization"), Runtime: q.Get("runtime"), Backend: q.Get("backend")}
	probe := schema.Rating{SchemaVersion: schema.Version, ClientID: strings.Repeat("0", 32), Stars: 3,
		Model: schema.Model{ID: c.Model, Format: c.Format, Quantization: c.Quantization}, Runtime: schema.Runtime{Type: c.Runtime, Backend: c.Backend},
		Hardware: schema.Hardware{Platform: "linux", Architecture: "amd64", Vendor: "cpu", Family: "probe", MemoryType: "system", MemoryBucket: "8-16"}}
	if err := probe.Validate(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.Store.ForConfig(r.Context(), c)
	if err != nil {
		s.Log.Error("read ratings", "err", err)
		fail(w, http.StatusInternalServerError, "ratings could not be read")
		return
	}
	mean, n, err := s.Store.Mean(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "ratings could not be read")
		return
	}
	prior := aggregate.Prior(mean, n)
	resp := modelResponse{SchemaVersion: schema.Version, Model: c.Model, Format: c.Format, Quantization: c.Quantization, Runtime: c.Runtime, Backend: c.Backend, Tiers: []aggregate.Stats{}}
	if hw := q.Get("hardware"); hw != "" {
		h, err := schema.ParseKey(hw)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		resp.Tiers = aggregate.ForHardware(rows, h, c.Backend, prior)
	} else if len(rows) > 0 {
		resp.Tiers = []aggregate.Stats{aggregate.Global(rows, prior)}
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, resp)
}

// aggregates is the public snapshot of every configuration.
func (s *Server) aggregates(w http.ResponseWriter, r *http.Request) {
	snap, err := s.Snapshot(r)
	if err != nil {
		s.Log.Error("aggregate", "err", err)
		fail(w, http.StatusInternalServerError, "aggregates could not be made")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// Snapshot builds the public aggregate snapshot.
func (s *Server) Snapshot(r *http.Request) (aggregate.Snapshot, error) {
	rows, err := s.Store.All(r.Context())
	if err != nil {
		return aggregate.Snapshot{}, err
	}
	mean, n, err := s.Store.Mean(r.Context())
	if err != nil {
		return aggregate.Snapshot{}, err
	}
	return aggregate.Public(rows, aggregate.Prior(mean, n), s.now()), nil
}

func (s *Server) batches(w http.ResponseWriter, r *http.Request) {
	since := s.now().Add(-24 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			fail(w, http.StatusBadRequest, "since must be an RFC 3339 time")
			return
		}
		since = t
	}
	list, err := s.Store.Batches(r.Context(), since)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) invalidate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Batch string `json:"batch"`
		Key   string `json:"key"`
	}
	if !decode(w, r, &req) {
		return
	}
	n, err := s.Store.Invalidate(r.Context(), req.Batch, req.Key)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Log.Info("invalidated ratings", "batch", req.Batch, "key", req.Key, "count", n)
	writeJSON(w, http.StatusOK, map[string]int64{"invalidated": n})
}
