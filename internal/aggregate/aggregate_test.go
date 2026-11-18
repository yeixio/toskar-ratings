package aggregate

import (
	"testing"

	"github.com/yeixio/toskar-ratings/internal/schema"
	"github.com/yeixio/toskar-ratings/internal/store"
)

func TestConfidence(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, Limited},
		{1, Limited},
		{2, Limited},
		{3, Early},
		{9, Early},
		{10, Community},
		{100, Community},
	}
	for _, tt := range tests {
		if got := confidence(tt.n); got != tt.want {
			t.Errorf("confidence(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestPrior(t *testing.T) {
	tests := []struct {
		mean float64
		n    int
		want float64
	}{
		{4.2, 0, DefaultPrior},
		{4.2, 49, DefaultPrior},
		{4.2, 50, 4.2},
		{3.8, 100, 3.8},
	}
	for _, tt := range tests {
		if got := Prior(tt.mean, tt.n); got != tt.want {
			t.Errorf("Prior(%v, %d) = %v, want %v", tt.mean, tt.n, got, tt.want)
		}
	}
}

func TestMedian(t *testing.T) {
	tests := []struct {
		in   []float64
		want float64
	}{
		{[]float64{1}, 1},
		{[]float64{1, 3}, 2},
		{[]float64{1, 2, 3}, 2},
		{[]float64{5, 1, 3, 2}, 2.5},
		{[]float64{}, 0}, // edge, though not expected in production
	}
	for _, tt := range tests {
		// copy so sort doesn't affect later
		in := append([]float64(nil), tt.in...)
		if got := median(in); got != tt.want {
			t.Errorf("median(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestSummarize(t *testing.T) {
	rows := []store.Row{
		{Stars: 5},
	}
	got := Summarize(schema.TierGlobal, "", rows, DefaultPrior)
	if got.Ratings != 1 {
		t.Errorf("Ratings = %d, want 1", got.Ratings)
	}
	if got.Average != 5 {
		t.Errorf("Average = %v, want 5", got.Average)
	}
	// (5 * 3.5 + 5) / (5 + 1) = 22.5 / 6 = 3.75
	if got.WeightedScore != 3.75 {
		t.Errorf("WeightedScore = %v, want 3.75", got.WeightedScore)
	}
	if got.Confidence != Limited {
		t.Errorf("Confidence = %q, want %q", got.Confidence, Limited)
	}

	// Many high ratings should score higher than a single 5.
	many := make([]store.Row, 20)
	for i := range many {
		many[i] = store.Row{Stars: 5}
	}
	gotMany := Summarize(schema.TierGlobal, "", many, DefaultPrior)
	if gotMany.WeightedScore <= got.WeightedScore {
		t.Errorf("many 5s WeightedScore %v should exceed single 5's %v", gotMany.WeightedScore, got.WeightedScore)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	got := Summarize(schema.TierGlobal, "cohort", nil, DefaultPrior)
	if got.Ratings != 0 || got.Confidence != Limited {
		t.Errorf("empty Summarize = %+v", got)
	}
}
