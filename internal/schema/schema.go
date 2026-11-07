// Package schema is the community ratings contract, version 1: what a
// rating says, how it is checked, and how hardware is grouped. Toskar
// Core sends ratings in this shape; the aggregates published to
// yeixio/toskar-model-data follow schema/ratings-v1.schema.json.
package schema

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Version is the schema version.
const Version = 1

// Model is what a rating is about. Ratings of different formats,
// quantizations, runtimes, or backends are never combined.
type Model struct {
	// ID is the base model as a lower-case slug, such as
	// qwen2.5-coder-7b-instruct: the same whoever converted it.
	ID           string `json:"id"`
	Format       string `json:"format"`
	Quantization string `json:"quantization"`
}

// Runtime is what ran the model.
type Runtime struct {
	Type    string `json:"type"`
	Backend string `json:"backend"`
	Version string `json:"version,omitempty"`
}

// Hardware is a computer's, phone's, or tablet's normalized class: never a
// name, device model, serial, or address.
type Hardware struct {
	// Platform is macos, linux, or windows for a computer, and ios, ipados,
	// or android for a phone or tablet.
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
	// Vendor is apple, nvidia, amd, intel, or cpu (no accelerator) on a
	// computer. On a phone or tablet it is the chip maker: apple on ios and
	// ipados; qualcomm, samsung, google, or mediatek on android.
	Vendor string `json:"vendor"`
	// Family is the accelerator model, such as m4-max or rtx-4090, or for
	// cpu, the CPU vendor. On a phone or tablet it is the chip, such as
	// a17-pro or snapdragon-8-gen-3, never the device model.
	Family string `json:"family"`
	// MemoryType is unified, dedicated, or system. Phones and tablets are
	// unified.
	MemoryType string `json:"memory_type"`
	// MemoryBucket is the memory the model can use, in GB: 0-8, 8-16,
	// 16-32, 32-64, 64-128, or 128+ on a computer. A phone or tablet gives
	// its memory, rounded to the nearest GB, as 0-4, 4-8, 8-12, 12-16,
	// 16-24, or 24+ (each band includes its lower bound).
	MemoryBucket string `json:"memory_bucket_gb"`
}

// Observations are optional runtime facts the person chose to share.
type Observations struct {
	TokensPerSecond float64 `json:"tokens_per_second,omitempty"`
	TTFTMillis      int     `json:"ttft_ms,omitempty"`
	// Starts and StartFailures count model starts.
	Starts        int  `json:"starts,omitempty"`
	StartFailures int  `json:"start_failures,omitempty"`
	Crashed       bool `json:"crashed,omitempty"`
	OutOfMemory   bool `json:"out_of_memory,omitempty"`
	// ContextBand is the context window used: 0-8k, 8-32k, 32-128k, 128k+.
	ContextBand string `json:"context_band,omitempty"`
}

// Rating is one submission.
type Rating struct {
	SchemaVersion int           `json:"schema_version"`
	ClientID      string        `json:"client_id"`
	Model         Model         `json:"model"`
	Runtime       Runtime       `json:"runtime"`
	Hardware      Hardware      `json:"hardware"`
	Stars         int           `json:"stars"`
	Tags          []string      `json:"tags,omitempty"`
	Observations  *Observations `json:"observations,omitempty"`
	AppVersion    string        `json:"app_version,omitempty"`
	// Language is the language the person used the model in, as a base
	// tag such as es, or zh-Hans or zh-Hant for Chinese, when they chose to
	// say. It is never combined with hardware in what is published.
	Language string `json:"language,omitempty"`
}

// Tags are the structured reasons a rating may give.
var Tags = []string{"great_responses", "fast", "slow", "stable", "crashed", "too_much_memory",
	"great_for_coding", "great_for_chat", "good_tool_use", "poor_tool_use"}

var (
	Formats  = []string{"gguf", "mlx", "safetensors", "onnx"}
	Runtimes = []string{"llamacpp", "mlx", "vllm", "ollama", "external", PhoneRuntime}
	Backends = []string{"metal", "cuda", "rocm", "vulkan", "sycl", "opencl", "cpu"}
	// Platforms are computers' then phones' and tablets'.
	Platforms = append(slices.Clone(ComputerPlatforms), PhonePlatforms...)
	Archs     = []string{"arm64", "amd64"}
	Vendors   = []string{"apple", "nvidia", "amd", "intel", "cpu", "qualcomm", "samsung", "google", "mediatek"}
	Memories  = []string{"unified", "dedicated", "system"}
	// Buckets are computers' memory bands then phones' and tablets'. The
	// two never overlap, so a cohort key says which kind of device it is.
	Buckets  = append(slices.Clone(ComputerBuckets), PhoneBuckets...)
	Contexts = []string{"0-8k", "8-32k", "32-128k", "128k+"}
)

// Phones and tablets. Their ratings run on PhoneRuntime, so a phone's and
// a computer's ratings of the same GGUF are never combined.
const PhoneRuntime = "llama.rn"

var (
	ComputerPlatforms = []string{"macos", "linux", "windows"}
	PhonePlatforms    = []string{"ios", "ipados", "android"}
	ComputerBuckets   = []string{"0-8", "8-16", "16-32", "32-64", "64-128", "128+"}
	PhoneBuckets      = []string{"0-4", "4-8", "8-12", "12-16", "16-24", "24+"}
	// PhoneBackends are what llama.rn runs on.
	PhoneBackends = []string{"metal", "opencl", "cpu"}
	// phoneVendors are the chip makers each phone platform allows.
	phoneVendors = map[string][]string{
		"ios":     {"apple"},
		"ipados":  {"apple"},
		"android": {"qualcomm", "samsung", "google", "mediatek"},
	}
	// phoneChips are the chip names each phone vendor allows. Only chips
	// pass, so a device model name such as pixel-8 or sm-s928b is refused
	// and never stored.
	phoneChips = map[string]*regexp.Regexp{
		"apple":    regexp.MustCompile(`^(a\d{1,2}[xz]?(-pro|-bionic)?|m\d{1,2}(-pro|-max|-ultra)?)$`),
		"qualcomm": regexp.MustCompile(`^snapdragon-(\d{3}g?(-plus)?|\d(s|-plus)?-(gen-\d|elite(-gen-\d)?))$`),
		"samsung":  regexp.MustCompile(`^exynos-\d{3,4}$`),
		"google":   regexp.MustCompile(`^tensor(-g\d{1,2})?$`),
		"mediatek": regexp.MustCompile(`^(dimensity-\d{3,4}(-plus|-ultra|-max)?|helio-[agpx]\d{2,3})$`),
	}
	// phoneVersionRe is a phone rating's app and runtime version: numbers
	// only, so nothing else, such as a device model, rides along in them.
	phoneVersionRe = regexp.MustCompile(`^\d{1,6}(\.\d{1,6}){1,3}$`)
)

// IsPhone reports whether a platform is a phone's or tablet's.
func IsPhone(platform string) bool { return slices.Contains(PhonePlatforms, platform) }

var (
	modelRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,99}$`)
	quantRe   = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_.-]{0,23}$`)
	familyRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	clientRe  = regexp.MustCompile(`^[a-f0-9]{32,64}$`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)
	// languageRe is a base language tag, or Chinese with its script.
	languageRe = regexp.MustCompile(`^([a-z]{2,3}|zh-Hans|zh-Hant)$`)
)

// ValidLanguage reports a language a rating may give; "" is none.
func ValidLanguage(l string) bool { return l == "" || languageRe.MatchString(l) }

func oneOf(field, v string, allowed []string) error {
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("%s must be one of %s", field, strings.Join(allowed, ", "))
	}
	return nil
}

// Validate checks a rating, rejecting impossible values.
func (r Rating) Validate() error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if r.SchemaVersion != Version {
		add(fmt.Errorf("schema_version must be %d", Version))
	}
	if !clientRe.MatchString(r.ClientID) {
		add(errors.New("client_id must be 32 to 64 random hex characters"))
	}
	if !modelRe.MatchString(r.Model.ID) {
		add(errors.New("model.id must be a lower-case slug"))
	}
	add(oneOf("model.format", r.Model.Format, Formats))
	if !quantRe.MatchString(r.Model.Quantization) {
		add(errors.New("model.quantization must be like Q4_K_M"))
	}
	add(oneOf("runtime.type", r.Runtime.Type, Runtimes))
	add(oneOf("runtime.backend", r.Runtime.Backend, Backends))
	if r.Runtime.Version != "" && !versionRe.MatchString(r.Runtime.Version) {
		add(errors.New("runtime.version is not a version"))
	}
	add(r.Hardware.Validate())
	phone := IsPhone(r.Hardware.Platform)
	switch {
	case phone && r.Runtime.Type != PhoneRuntime:
		add(fmt.Errorf("runtime.type must be %s on a phone or tablet", PhoneRuntime))
	case phone:
		add(oneOf("runtime.backend", r.Runtime.Backend, PhoneBackends))
	case r.Runtime.Type == PhoneRuntime:
		add(fmt.Errorf("runtime.type %s is only for phones and tablets", PhoneRuntime))
	}
	if phone {
		if r.Runtime.Version != "" && !phoneVersionRe.MatchString(r.Runtime.Version) {
			add(errors.New("runtime.version must be numbers such as 0.6.3 on a phone or tablet"))
		}
		if r.AppVersion != "" && !phoneVersionRe.MatchString(r.AppVersion) {
			add(errors.New("app_version must be numbers such as 1.12.1 on a phone or tablet"))
		}
	}
	if r.Stars < 1 || r.Stars > 5 {
		add(errors.New("stars must be 1 to 5"))
	}
	if len(r.Tags) > len(Tags) {
		add(errors.New("too many tags"))
	}
	seen := map[string]bool{}
	for _, t := range r.Tags {
		if !slices.Contains(Tags, t) || seen[t] {
			add(fmt.Errorf("unknown or repeated tag %q", t))
		}
		seen[t] = true
	}
	if o := r.Observations; o != nil {
		if o.TokensPerSecond < 0 || o.TokensPerSecond > 10000 {
			add(errors.New("observations.tokens_per_second is impossible"))
		}
		if o.TTFTMillis < 0 || o.TTFTMillis > 600000 {
			add(errors.New("observations.ttft_ms is impossible"))
		}
		if o.Starts < 0 || o.StartFailures < 0 || o.StartFailures > o.Starts || o.Starts > 100000 {
			add(errors.New("observations.starts is impossible"))
		}
		if o.ContextBand != "" {
			add(oneOf("observations.context_band", o.ContextBand, Contexts))
		}
	}
	if r.AppVersion != "" && !versionRe.MatchString(r.AppVersion) {
		add(errors.New("app_version is not a version"))
	}
	if !ValidLanguage(r.Language) {
		add(errors.New("language must be a base language tag such as es, or zh-Hans or zh-Hant"))
	}
	return errors.Join(errs...)
}

// Validate checks a hardware class.
func (h Hardware) Validate() error {
	var errs []error
	for _, e := range []error{
		oneOf("hardware.platform", h.Platform, Platforms),
		oneOf("hardware.architecture", h.Architecture, Archs),
		oneOf("hardware.vendor", h.Vendor, Vendors),
		oneOf("hardware.memory_type", h.MemoryType, Memories),
		oneOf("hardware.memory_bucket_gb", h.MemoryBucket, Buckets),
	} {
		if e != nil {
			errs = append(errs, e)
		}
	}
	if !familyRe.MatchString(h.Family) {
		errs = append(errs, errors.New("hardware.family must be a short slug such as m4-max"))
	}
	if IsPhone(h.Platform) {
		if allowed := phoneVendors[h.Platform]; !slices.Contains(allowed, h.Vendor) {
			errs = append(errs, fmt.Errorf("hardware.vendor must be one of %s on %s", strings.Join(allowed, ", "), h.Platform))
		} else if !phoneChips[h.Vendor].MatchString(h.Family) {
			errs = append(errs, errors.New("hardware.family must be a chip such as a17-pro or snapdragon-8-gen-3, never a device model"))
		}
		if h.MemoryType != "unified" {
			errs = append(errs, errors.New("hardware.memory_type must be unified on a phone or tablet"))
		}
		if slices.Contains(ComputerBuckets, h.MemoryBucket) {
			errs = append(errs, fmt.Errorf("hardware.memory_bucket_gb must be one of %s on a phone or tablet", strings.Join(PhoneBuckets, ", ")))
		}
	} else if slices.Contains(ComputerPlatforms, h.Platform) {
		if slices.Contains(phoneVendors["android"], h.Vendor) {
			errs = append(errs, fmt.Errorf("hardware.vendor %s is only for phones and tablets", h.Vendor))
		}
		if slices.Contains(PhoneBuckets, h.MemoryBucket) {
			errs = append(errs, fmt.Errorf("hardware.memory_bucket_gb must be one of %s on a computer", strings.Join(ComputerBuckets, ", ")))
		}
	}
	return errors.Join(errs...)
}
