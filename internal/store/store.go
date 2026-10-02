// Package store keeps ratings in SQLite: one current rating per
// pseudonymous client and configuration. Client IDs are kept only as a
// keyed hash, and no address or name is stored.
package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	_ "modernc.org/sqlite" // the database driver

	"github.com/yeixio/yggdrasil-ratings/internal/schema"
)

// Errors.
var (
	ErrNotFound  = errors.New("no such rating")
	ErrForbidden = errors.New("that rating belongs to another client")
)

// Store is the ratings database.
type Store struct {
	db     *sql.DB
	secret []byte
	Now    func() time.Time
}

// Open opens or creates the database. secret keys the client hash and must
// stay the same across restarts.
func Open(path string, secret []byte) (*Store, error) {
	if len(secret) < 16 {
		return nil, errors.New("the secret must be at least 16 bytes")
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, secret: secret, Now: time.Now}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS ratings (
	key TEXT PRIMARY KEY,
	client_hash TEXT NOT NULL,
	model TEXT NOT NULL,
	format TEXT NOT NULL,
	quantization TEXT NOT NULL,
	runtime TEXT NOT NULL,
	backend TEXT NOT NULL,
	tier0 TEXT NOT NULL,
	tier1 TEXT NOT NULL,
	tier2 TEXT NOT NULL,
	tier3 TEXT NOT NULL,
	stars INTEGER NOT NULL CHECK (stars BETWEEN 1 AND 5),
	tags TEXT NOT NULL DEFAULT '[]',
	observations TEXT,
	app_version TEXT NOT NULL DEFAULT '',
	runtime_version TEXT NOT NULL DEFAULT '',
	batch TEXT NOT NULL DEFAULT '',
	invalid INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ratings_config ON ratings (model, format, quantization, runtime, backend);
CREATE INDEX IF NOT EXISTS ratings_batch ON ratings (batch);`)
	return err
}

// ClientHash is the stored form of a client ID.
func (s *Store) ClientHash(clientID string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte("client:" + clientID))
	return hex.EncodeToString(m.Sum(nil))
}

// Key identifies one client's rating of one configuration.
func (s *Store) Key(r schema.Rating) string {
	m := hmac.New(sha256.New, s.secret)
	for _, p := range []string{"key", r.ClientID, r.Model.ID, r.Model.Format, r.Model.Quantization, r.Runtime.Type, r.Runtime.Backend, r.Hardware.Key()} {
		m.Write([]byte(p + "\x00"))
	}
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func (s *Store) now() string { return s.Now().UTC().Format(time.RFC3339) }

// Put stores a rating, replacing the client's earlier rating of the same
// configuration. It reports whether the rating is new.
func (s *Store) Put(ctx context.Context, r schema.Rating, batch string) (string, bool, error) {
	key := s.Key(r)
	tags, _ := json.Marshal(nonNil(r.Tags))
	var obs any
	if r.Observations != nil {
		b, _ := json.Marshal(r.Observations)
		obs = string(b)
	}
	c := schema.Cohorts(r.Hardware, r.Runtime.Backend)
	now := s.now()
	res, err := s.db.ExecContext(ctx, `
INSERT INTO ratings (key, client_hash, model, format, quantization, runtime, backend, tier0, tier1, tier2, tier3, stars, tags, observations, app_version, runtime_version, batch, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO NOTHING`,
		key, s.ClientHash(r.ClientID), r.Model.ID, r.Model.Format, r.Model.Quantization, r.Runtime.Type, r.Runtime.Backend,
		c[0], c[1], c[2], c[3], r.Stars, string(tags), obs, r.AppVersion, r.Runtime.Version, batch, now, now)
	if err != nil {
		return "", false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return key, true, nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE ratings SET stars = ?, tags = ?, observations = ?, app_version = ?, runtime_version = ?, batch = ?, invalid = 0, updated_at = ? WHERE key = ?`,
		r.Stars, string(tags), obs, r.AppVersion, r.Runtime.Version, batch, now, key)
	return key, false, err
}

func nonNil(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

func (s *Store) owner(ctx context.Context, key, clientID string) error {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT client_hash FROM ratings WHERE key = ?`, key).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(hash), []byte(s.ClientHash(clientID))) {
		return ErrForbidden
	}
	return nil
}

// Update changes the stars, tags, and observations of a client's rating.
func (s *Store) Update(ctx context.Context, key, clientID string, stars int, tags []string, obs *schema.Observations, batch string) error {
	if err := s.owner(ctx, key, clientID); err != nil {
		return err
	}
	t, _ := json.Marshal(nonNil(tags))
	var o any
	if obs != nil {
		b, _ := json.Marshal(obs)
		o = string(b)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE ratings SET stars = ?, tags = ?, observations = ?, batch = ?, updated_at = ? WHERE key = ?`, stars, string(t), o, batch, s.now(), key)
	return err
}

// Delete removes a client's rating.
func (s *Store) Delete(ctx context.Context, key, clientID string) error {
	if err := s.owner(ctx, key, clientID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM ratings WHERE key = ?`, key)
	return err
}

// Row is a stored rating, without who gave it.
type Row struct {
	Model, Format, Quantization, Runtime, Backend string
	Tiers                                         [4]string
	Stars                                         int
	Tags                                          []string
	Observations                                  *schema.Observations
}

// Config is the model configuration ratings are grouped by.
type Config struct {
	Model, Format, Quantization, Runtime, Backend string
}

func (s *Store) rows(ctx context.Context, where string, args ...any) ([]Row, error) {
	q := `SELECT model, format, quantization, runtime, backend, tier0, tier1, tier2, tier3, stars, tags, COALESCE(observations, '') FROM ratings WHERE invalid = 0`
	if where != "" {
		q += " AND " + where
	}
	rs, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []Row
	for rs.Next() {
		var r Row
		var tags, obs string
		if err := rs.Scan(&r.Model, &r.Format, &r.Quantization, &r.Runtime, &r.Backend, &r.Tiers[0], &r.Tiers[1], &r.Tiers[2], &r.Tiers[3], &r.Stars, &tags, &obs); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &r.Tags)
		if obs != "" {
			r.Observations = &schema.Observations{}
			_ = json.Unmarshal([]byte(obs), r.Observations)
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// ForConfig returns the valid ratings of one configuration.
func (s *Store) ForConfig(ctx context.Context, c Config) ([]Row, error) {
	return s.rows(ctx, `model = ? AND format = ? AND quantization = ? AND runtime = ? AND backend = ?`, c.Model, c.Format, c.Quantization, c.Runtime, c.Backend)
}

// All returns every valid rating.
func (s *Store) All(ctx context.Context) ([]Row, error) { return s.rows(ctx, "") }

// Mean is the average of every valid rating, the prior for weighted
// scores; ok is false with too few ratings to be one.
func (s *Store) Mean(ctx context.Context) (float64, int, error) {
	var mean sql.NullFloat64
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT AVG(stars), COUNT(*) FROM ratings WHERE invalid = 0`).Scan(&mean, &n)
	return mean.Float64, n, err
}

// Batch is a group of writes, for spotting floods.
type Batch struct {
	Batch   string `json:"batch"`
	Ratings int    `json:"ratings"`
	Invalid int    `json:"invalid"`
}

// Batches lists the batches written since a time, largest first.
func (s *Store) Batches(ctx context.Context, since time.Time) ([]Batch, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT batch, COUNT(*), SUM(invalid) FROM ratings WHERE updated_at >= ? GROUP BY batch ORDER BY COUNT(*) DESC LIMIT 200`,
		since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := []Batch{}
	for rs.Next() {
		var b Batch
		if err := rs.Scan(&b.Batch, &b.Ratings, &b.Invalid); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rs.Err()
}

// Invalidate marks a batch's ratings, or one rating by key, as invalid, so
// they no longer count. It returns how many changed.
func (s *Store) Invalidate(ctx context.Context, batch, key string) (int64, error) {
	var res sql.Result
	var err error
	switch {
	case key != "":
		res, err = s.db.ExecContext(ctx, `UPDATE ratings SET invalid = 1 WHERE key = ?`, key)
	case batch != "":
		res, err = s.db.ExecContext(ctx, `UPDATE ratings SET invalid = 1 WHERE batch = ?`, batch)
	default:
		return 0, errors.New("give a batch or a key")
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
