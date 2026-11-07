package schema

import (
	"errors"
	"regexp"
	"slices"
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

// ParseKey reads an exact cohort key back. The key has no platform, so it
// takes one the key fits: a phone's or tablet's when the memory band is a
// phone band (ios for apple, else android), else a computer's (macos for
// apple, else linux). Nothing downstream depends on which one.
func ParseKey(key string) (Hardware, error) {
	p := strings.Split(key, ":")
	if len(p) != 4 {
		return Hardware{}, errors.New("hardware must be vendor:family:memory_type:memory_bucket_gb")
	}
	h := Hardware{Platform: "linux", Architecture: "amd64", Vendor: p[0], Family: p[1], MemoryType: p[2], MemoryBucket: p[3]}
	switch phone := slices.Contains(PhoneBuckets, h.MemoryBucket); {
	case phone && h.Vendor == "apple":
		h.Platform, h.Architecture = "ios", "arm64"
	case phone:
		h.Platform, h.Architecture = "android", "arm64"
	case h.Vendor == "apple":
		h.Platform, h.Architecture = "macos", "arm64"
	}
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
	// Phone and tablet chips.
	aSeriesRe    = regexp.MustCompile(`^a\d`)
	snapdragonRe = regexp.MustCompile(`^snapdragon-(\d)`)
	exynosRe     = regexp.MustCompile(`^exynos-(\d)(\d+)$`)
	dimensityRe  = regexp.MustCompile(`^dimensity-(\d)(\d+)`)
)

// Class is the accelerator class a family belongs to, such as
// apple-silicon, rtx-40, or rx-7000, or for a phone or tablet chip,
// a-series, snapdragon-8, exynos-2000, tensor, or dimensity-9000.
func Class(vendor, family string) string {
	switch vendor {
	case "apple":
		if appleRe.MatchString(family) {
			return "apple-silicon"
		}
		if aSeriesRe.MatchString(family) {
			return "a-series"
		}
	case "qualcomm":
		// The series digit: 8 is the flagships, then 7, 6, and 4.
		if m := snapdragonRe.FindStringSubmatch(family); m != nil {
			return "snapdragon-" + m[1]
		}
	case "samsung":
		// exynos-2400 is in exynos-2000, exynos-990 in exynos-900, and the
		// same for dimensity below.
		if m := exynosRe.FindStringSubmatch(family); m != nil {
			return "exynos-" + m[1] + strings.Repeat("0", len(m[2]))
		}
	case "google":
		if strings.HasPrefix(family, "tensor") {
			return "tensor"
		}
	case "mediatek":
		if m := dimensityRe.FindStringSubmatch(family); m != nil {
			return "dimensity-" + m[1] + strings.Repeat("0", len(m[2]))
		}
		if strings.HasPrefix(family, "helio-") {
			return "helio"
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
