// Package schema is the community ratings contract, version 1: what a
// rating says, how it is checked, and how hardware is grouped. Yggdrasil
// Core sends ratings in this shape; the aggregates published to
// yeixio/yggdrasil-model-data follow schema/ratings-v1.schema.json.
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

// Hardware is a computer's normalized class: never a name, serial, or
// address.
type Hardware struct {
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
	// Vendor is apple, nvidia, amd, intel, or cpu (no accelerator).
	Vendor string `json:"vendor"`
	// Family is the accelerator model, such as m4-max or rtx-4090, or for
	// cpu, the CPU vendor.
	Family string `json:"family"`
	// MemoryType is unified, dedicated, or system.
	MemoryType string `json:"memory_type"`
	// MemoryBucket is the memory the model can use, in GB: 0-8, 8-16,
	// 16-32, 32-64, 64-128, or 128+.
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
	Formats   = []string{"gguf", "mlx", "safetensors", "onnx"}
	Runtimes  = []string{"llamacpp", "mlx", "vllm", "ollama", "external"}
	Backends  = []string{"metal", "cuda", "rocm", "vulkan", "sycl", "cpu"}
	Platforms = []string{"macos", "linux", "windows"}
	Archs     = []string{"arm64", "amd64"}
	Vendors   = []string{"apple", "nvidia", "amd", "intel", "cpu"}
	Memories  = []string{"unified", "dedicated", "system"}
	Buckets   = []string{"0-8", "8-16", "16-32", "32-64", "64-128", "128+"}
	Contexts  = []string{"0-8k", "8-32k", "32-128k", "128k+"}
)

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
	return errors.Join(errs...)
}
