// Package aggregate turns ratings into what clients see: per-cohort
// averages, confidence-weighted scores, and observation medians.
package aggregate

import (
	"math"
	"sort"
	"time"

	"github.com/yeixio/toskar-ratings/internal/schema"
	"github.com/yeixio/toskar-ratings/internal/store"
)

// Weight is how many ratings' worth the prior counts for: a model with a
// few ratings is pulled toward the overall mean (a Bayesian average).
const Weight = 5

// DefaultPrior is the mean used before there are enough ratings to have
// one.
const DefaultPrior = 3.5

// MinPublic is the fewest ratings a cohort needs to appear in public
// aggregates, so no one's rating can be singled out.
const MinPublic = 3

// Confidence labels by sample size.
const (
	Limited   = "limited"   // 1–2 ratings
	Early     = "early"     // 3–9
	Community = "community" // 10+
)

// Stats are one cohort's ratings.
type Stats struct {
	Tier          string         `json:"tier"`
	Cohort        string         `json:"cohort,omitempty"`
	Ratings       int            `json:"ratings"`
	Average       float64        `json:"average"`
	WeightedScore float64        `json:"weighted_score"`
	Confidence    string         `json:"confidence"`
	Tags          map[string]int `json:"tags,omitempty"`
	// Observation summaries, from the ratings that shared them.
	Observed              int      `json:"observed,omitempty"`
	MedianTokensPerSecond *float64 `json:"median_tokens_per_second,omitempty"`
	MedianTTFTMillis      *float64 `json:"median_ttft_ms,omitempty"`
	SuccessfulStartRate   *float64 `json:"successful_start_rate,omitempty"`
	CrashRate             *float64 `json:"crash_rate,omitempty"`
	OutOfMemoryRate       *float64 `json:"out_of_memory_rate,omitempty"`
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func median(v []float64) float64 {
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

func confidence(n int) string {
	switch {
	case n >= 10:
		return Community
	case n >= 3:
		return Early
	}
	return Limited
}

// Summarize computes a cohort's stats. prior is the mean the weighted
// score leans toward.
func Summarize(tier int, cohort string, rows []store.Row, prior float64) Stats {
	s := Stats{Tier: schema.TierNames[tier], Cohort: cohort, Ratings: len(rows), Confidence: confidence(len(rows))}
	if len(rows) == 0 {
		return s
	}
	sum := 0
	tags := map[string]int{}
	var tps, ttft []float64
	var starts, failures, crashes, ooms int
	for _, r := range rows {
		sum += r.Stars
		for _, t := range r.Tags {
			tags[t]++
		}
		if o := r.Observations; o != nil {
			s.Observed++
			if o.TokensPerSecond > 0 {
				tps = append(tps, o.TokensPerSecond)
			}
			if o.TTFTMillis > 0 {
				ttft = append(ttft, float64(o.TTFTMillis))
			}
			starts += o.Starts
			failures += o.StartFailures
			if o.Crashed {
				crashes++
			}
			if o.OutOfMemory {
				ooms++
			}
		}
	}
	n := float64(len(rows))
	s.Average = round(float64(sum)/n, 2)
	s.WeightedScore = round((Weight*prior+float64(sum))/(Weight+n), 2)
	if len(tags) > 0 {
		s.Tags = tags
	}
	if len(tps) > 0 {
		v := round(median(tps), 1)
		s.MedianTokensPerSecond = &v
	}
	if len(ttft) > 0 {
		v := math.Round(median(ttft))
		s.MedianTTFTMillis = &v
	}
	if starts > 0 {
		v := round(float64(starts-failures)/float64(starts), 3)
		s.SuccessfulStartRate = &v
	}
	if s.Observed > 0 {
		c, o := round(float64(crashes)/float64(s.Observed), 3), round(float64(ooms)/float64(s.Observed), 3)
		s.CrashRate, s.OutOfMemoryRate = &c, &o
	}
	return s
}

// Prior is the mean weighted scores lean toward: the overall mean once
// there are enough ratings, else DefaultPrior.
func Prior(mean float64, n int) float64 {
	if n < 50 {
		return DefaultPrior
	}
	return mean
}

// ForHardware is a configuration's stats at every tier, narrowest first,
// for a client's exact cohort. Tiers without ratings are left out.
func ForHardware(rows []store.Row, h schema.Hardware, backend string, prior float64) []Stats {
	keys := schema.Cohorts(h, backend)
	var out []Stats
	for tier := schema.TierExact; tier <= schema.TierGlobal; tier++ {
		var in []store.Row
		for _, r := range rows {
			if tier == schema.TierGlobal || r.Tiers[tier] == keys[tier] {
				in = append(in, r)
			}
		}
		if len(in) == 0 {
			continue
		}
		cohort := ""
		if tier < schema.TierGlobal {
			cohort = keys[tier]
		}
		out = append(out, Summarize(tier, cohort, in, prior))
	}
	return out
}

// Global is a configuration's stats over everyone.
func Global(rows []store.Row, prior float64) Stats {
	return Summarize(schema.TierGlobal, "", rows, prior)
}

// ModelAggregate is one configuration in the public snapshot.
type ModelAggregate struct {
	Model        string  `json:"model"`
	Format       string  `json:"format"`
	Quantization string  `json:"quantization"`
	Runtime      string  `json:"runtime"`
	Backend      string  `json:"backend"`
	Cohorts      []Stats `json:"cohorts"`
	// Languages are the configuration's ratings by the language it was used
	// in, each only with MinPublic ratings, and never split by hardware.
	Languages []LanguageStats `json:"languages,omitempty"`
}

// LanguageStats are a configuration's ratings given for one language.
type LanguageStats struct {
	Language      string  `json:"language"`
	Ratings       int     `json:"ratings"`
	Average       float64 `json:"average"`
	WeightedScore float64 `json:"weighted_score"`
	Confidence    string  `json:"confidence"`
}

// byLanguage summarizes the ratings that give a language, per language,
// leaving out languages with fewer than MinPublic ratings.
func byLanguage(rows []store.Row, prior float64) []LanguageStats {
	by := map[string][]store.Row{}
	for _, r := range rows {
		if r.Language != "" {
			by[r.Language] = append(by[r.Language], r)
		}
	}
	langs := make([]string, 0, len(by))
	for l := range by {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	var out []LanguageStats
	for _, l := range langs {
		if len(by[l]) < MinPublic {
			continue
		}
		s := Summarize(schema.TierGlobal, "", by[l], prior)
		out = append(out, LanguageStats{Language: l, Ratings: s.Ratings, Average: s.Average, WeightedScore: s.WeightedScore, Confidence: s.Confidence})
	}
	return out
}

// Snapshot is the public aggregate dataset (ratings-v1.schema.json).
type Snapshot struct {
	SchemaVersion int              `json:"schema_version"`
	GeneratedAt   string           `json:"generated_at"`
	Prior         float64          `json:"prior"`
	Weight        int              `json:"weight"`
	MinRatings    int              `json:"min_ratings"`
	Models        []ModelAggregate `json:"models"`
}

// Public builds the snapshot: every configuration with its family, class,
// backend, and global cohorts, each only when it has MinPublic ratings.
// Exact cohorts are left out: they are the most identifying.
func Public(rows []store.Row, prior float64, now time.Time) Snapshot {
	groups := map[store.Config][]store.Row{}
	for _, r := range rows {
		c := store.Config{Model: r.Model, Format: r.Format, Quantization: r.Quantization, Runtime: r.Runtime, Backend: r.Backend}
		groups[c] = append(groups[c], r)
	}
	snap := Snapshot{SchemaVersion: schema.Version, GeneratedAt: now.UTC().Format(time.RFC3339), Prior: round(prior, 2), Weight: Weight, MinRatings: MinPublic, Models: []ModelAggregate{}}
	for c, rs := range groups {
		if len(rs) < MinPublic {
			continue
		}
		m := ModelAggregate{Model: c.Model, Format: c.Format, Quantization: c.Quantization, Runtime: c.Runtime, Backend: c.Backend}
		for tier := schema.TierFamily; tier <= schema.TierBackend; tier++ {
			by := map[string][]store.Row{}
			for _, r := range rs {
				by[r.Tiers[tier]] = append(by[r.Tiers[tier]], r)
			}
			keys := make([]string, 0, len(by))
			for k := range by {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if len(by[k]) >= MinPublic {
					m.Cohorts = append(m.Cohorts, Summarize(tier, k, by[k], prior))
				}
			}
		}
		m.Cohorts = append(m.Cohorts, Global(rs, prior))
		m.Languages = byLanguage(rs, prior)
		snap.Models = append(snap.Models, m)
	}
	sort.Slice(snap.Models, func(i, j int) bool {
		a, b := snap.Models[i], snap.Models[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Quantization != b.Quantization {
			return a.Quantization < b.Quantization
		}
		return a.Runtime+a.Backend < b.Runtime+b.Backend
	})
	return snap
}
