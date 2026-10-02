package schema

import (
	"errors"
	"regexp"
	"strings"
)

// Tiers of similarity, narrowest first.
const (
	TierExact   = 0 // same accelerator model and memory band
	TierFamily  = 1 // same accelerator model
	TierClass   = 2 // same accelerator class and memory band
	TierBackend = 3 // same runtime backend and memory band
	TierGlobal  = 4 // everyone
)

// TierNames are how tiers are named in aggregates.
var TierNames = []string{"exact", "family", "class", "backend", "global"}

// Key is the exact cohort, such as apple:m4-max:unified:32-64. Core sends
// it to ask for ratings from hardware like its own.
func (h Hardware) Key() string {
	return h.Vendor + ":" + h.Family + ":" + h.MemoryType + ":" + h.MemoryBucket
}

// ParseKey reads an exact cohort key back.
func ParseKey(key string) (Hardware, error) {
	p := strings.Split(key, ":")
	if len(p) != 4 {
		return Hardware{}, errors.New("hardware must be vendor:family:memory_type:memory_bucket_gb")
	}
	h := Hardware{Platform: "linux", Architecture: "amd64", Vendor: p[0], Family: p[1], MemoryType: p[2], MemoryBucket: p[3]}
	if err := h.Validate(); err != nil {
		return Hardware{}, err
	}
	return h, nil
}

var (
	appleRe  = regexp.MustCompile(`^m\d+`)
	rtxRe    = regexp.MustCompile(`^rtx-(?:a?)(\d{2})\d{2}`)
	gtxRe    = regexp.MustCompile(`^(gtx|rtx)-(\d{1,2})`)
	radeonRe = regexp.MustCompile(`^rx-(\d)\d{3}`)
	arcRe    = regexp.MustCompile(`^arc-([ab])\d+`)
)

// Class is the accelerator class a family belongs to, such as
// apple-silicon, rtx-40, or rx-7000.
func Class(vendor, family string) string {
	switch vendor {
	case "apple":
		if appleRe.MatchString(family) {
			return "apple-silicon"
		}
	case "nvidia":
		if m := rtxRe.FindStringSubmatch(family); m != nil {
			return "rtx-" + m[1]
		}
		if m := gtxRe.FindStringSubmatch(family); m != nil {
			return m[1] + "-" + m[2]
		}
		return "nvidia-other"
	case "amd":
		if m := radeonRe.FindStringSubmatch(family); m != nil {
			return "rx-" + m[1] + "000"
		}
		return "amd-other"
	case "intel":
		if m := arcRe.FindStringSubmatch(family); m != nil {
			return "arc-" + m[1]
		}
		return "intel-other"
	case "cpu":
		return "cpu"
	}
	return vendor + "-other"
}

// Cohorts are a rating's cohort keys for tiers 0 to 3 (tier 4 is all).
func Cohorts(h Hardware, backend string) [4]string {
	return [4]string{
		h.Key(),
		h.Vendor + ":" + h.Family,
		h.Vendor + ":" + Class(h.Vendor, h.Family) + ":" + h.MemoryBucket,
		backend + ":" + h.MemoryBucket,
	}
}
