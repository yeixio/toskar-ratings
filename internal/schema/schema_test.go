package schema

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func valid() Rating {
	return Rating{SchemaVersion: 1, ClientID: strings.Repeat("ab", 16),
		Model:    Model{ID: "qwen2.5-coder-7b-instruct", Format: "gguf", Quantization: "Q4_K_M"},
		Runtime:  Runtime{Type: "llamacpp", Backend: "metal", Version: "b5000"},
		Hardware: Hardware{Platform: "macos", Architecture: "arm64", Vendor: "apple", Family: "m4-max", MemoryType: "unified", MemoryBucket: "32-64"},
		Stars:    4, Tags: []string{"fast", "great_for_coding"}, Observations: &Observations{TokensPerSecond: 42.5, Starts: 10, StartFailures: 1}}
}

func TestValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Rating){
		"stars":    func(r *Rating) { r.Stars = 6 },
		"client":   func(r *Rating) { r.ClientID = "my-laptop" },
		"model":    func(r *Rating) { r.Model.ID = "Qwen/Qwen2.5" },
		"quant":    func(r *Rating) { r.Model.Quantization = "q4 k m" },
		"backend":  func(r *Rating) { r.Runtime.Backend = "directx" },
		"tag":      func(r *Rating) { r.Tags = []string{"best model ever"} },
		"repeat":   func(r *Rating) { r.Tags = []string{"fast", "fast"} },
		"tps":      func(r *Rating) { r.Observations.TokensPerSecond = 1e6 },
		"failures": func(r *Rating) { r.Observations.StartFailures = 11 },
		"family":   func(r *Rating) { r.Hardware.Family = "John's MacBook" },
		"bucket":   func(r *Rating) { r.Hardware.MemoryBucket = "48" },
		"version":  func(r *Rating) { r.SchemaVersion = 2 },
	} {
		r := valid()
		change(&r)
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCohorts(t *testing.T) {
	h := valid().Hardware
	c := Cohorts(h, "metal")
	want := [4]string{"apple:m4-max:unified:32-64", "apple:m4-max", "apple:apple-silicon:32-64", "metal:32-64"}
	if c != want {
		t.Fatalf("cohorts %v", c)
	}
	back, err := ParseKey(c[0])
	if err != nil || back.Key() != c[0] {
		t.Fatalf("parse %v %v", back, err)
	}
	for _, k := range []string{"apple:m4-max", "apple:m4-max:unified:48", "x:y:z:w"} {
		if _, err := ParseKey(k); err == nil {
			t.Errorf("%s parsed", k)
		}
	}
	for v, f := range map[[2]string]string{
		{"nvidia", "rtx-4090"}: "rtx-40", {"nvidia", "rtx-3060"}: "rtx-30", {"nvidia", "rtx-a6000"}: "rtx-60", {"nvidia", "gtx-1080"}: "gtx-10",
		{"amd", "rx-7900-xtx"}: "rx-7000", {"intel", "arc-a770"}: "arc-a", {"apple", "m1"}: "apple-silicon", {"cpu", "amd"}: "cpu",
	} {
		if got := Class(v[0], v[1]); got != f {
			t.Errorf("Class(%s, %s) = %s, want %s", v[0], v[1], got, f)
		}
	}
}

func phone() Rating {
	r := valid()
	r.Runtime = Runtime{Type: "llama.rn", Backend: "opencl", Version: "0.6.3"}
	r.Hardware = Hardware{Platform: "android", Architecture: "arm64", Vendor: "qualcomm", Family: "snapdragon-8-gen-3", MemoryType: "unified", MemoryBucket: "12-16"}
	r.AppVersion = "1.12.1"
	return r
}

// Phones and tablets rate with their chip and a phone memory band, on
// llama.rn, and never with a device model.
func TestValidatePhone(t *testing.T) {
	for name, change := range map[string]func(*Rating){
		"android": func(r *Rating) {},
		"iphone": func(r *Rating) {
			r.Runtime.Backend = "metal"
			r.Hardware = Hardware{"ios", "arm64", "apple", "a17-pro", "unified", "8-12"}
		},
		"ipad m-series": func(r *Rating) {
			r.Runtime.Backend = "metal"
			r.Hardware = Hardware{"ipados", "arm64", "apple", "m4", "unified", "16-24"}
		},
		"old iphone": func(r *Rating) {
			r.Runtime.Backend = "cpu"
			r.Hardware = Hardware{"ios", "arm64", "apple", "a12-bionic", "unified", "0-4"}
		},
		"pixel": func(r *Rating) {
			r.Runtime.Backend = "cpu"
			r.Hardware.Vendor, r.Hardware.Family = "google", "tensor-g3"
		},
		"galaxy":      func(r *Rating) { r.Hardware.Vendor, r.Hardware.Family = "samsung", "exynos-2400" },
		"dimensity":   func(r *Rating) { r.Hardware.Vendor, r.Hardware.Family = "mediatek", "dimensity-9300-plus" },
		"helio":       func(r *Rating) { r.Hardware.Vendor, r.Hardware.Family = "mediatek", "helio-g99" },
		"8 elite":     func(r *Rating) { r.Hardware.Family = "snapdragon-8-elite" },
		"7+ gen 2":    func(r *Rating) { r.Hardware.Family = "snapdragon-7-plus-gen-2" },
		"888":         func(r *Rating) { r.Hardware.Family = "snapdragon-888" },
		"no versions": func(r *Rating) { r.Runtime.Version, r.AppVersion = "", "" },
	} {
		r := phone()
		change(&r)
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, change := range map[string]func(*Rating){
		// Device model names, in any field, are refused, so never stored.
		"pixel model":     func(r *Rating) { r.Hardware.Vendor, r.Hardware.Family = "google", "pixel-8" },
		"galaxy model":    func(r *Rating) { r.Hardware.Vendor, r.Hardware.Family = "samsung", "sm-s928b" },
		"galaxy name":     func(r *Rating) { r.Hardware.Family = "galaxy-s24-ultra" },
		"iphone model":    func(r *Rating) { r.Hardware = Hardware{"ios", "arm64", "apple", "iphone16-2", "unified", "8-12"} },
		"ipad model":      func(r *Rating) { r.Hardware = Hardware{"ipados", "arm64", "apple", "ipad14-1", "unified", "8-12"} },
		"chip and model":  func(r *Rating) { r.Hardware.Family = "snapdragon-8-gen-3-galaxy" },
		"model in app":    func(r *Rating) { r.AppVersion = "SM-S928B" },
		"model in rn":     func(r *Rating) { r.Runtime.Version = "Pixel-8" },
		"llamacpp":        func(r *Rating) { r.Runtime.Type = "llamacpp" },
		"cuda":            func(r *Rating) { r.Runtime.Backend = "cuda" },
		"computer band":   func(r *Rating) { r.Hardware.MemoryBucket = "8-16" },
		"dedicated":       func(r *Rating) { r.Hardware.MemoryType = "dedicated" },
		"apple android":   func(r *Rating) { r.Hardware.Vendor, r.Hardware.Family = "apple", "a17-pro" },
		"qualcomm on ios": func(r *Rating) { r.Hardware.Platform = "ios" },
		"cpu vendor":      func(r *Rating) { r.Hardware.Vendor, r.Hardware.Family = "cpu", "arm" },
	} {
		r := phone()
		change(&r)
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// And computers keep to computer runtimes, vendors, and bands.
	for name, change := range map[string]func(*Rating){
		"llama.rn":   func(r *Rating) { r.Runtime.Type = "llama.rn" },
		"phone chip": func(r *Rating) { r.Hardware.Vendor, r.Hardware.Family = "qualcomm", "snapdragon-8-gen-3" },
		"phone band": func(r *Rating) { r.Hardware.MemoryBucket = "8-12" },
	} {
		r := valid()
		change(&r)
		if r.Validate() == nil {
			t.Errorf("computer %s: accepted", name)
		}
	}
}

func TestPhoneCohorts(t *testing.T) {
	for _, c := range []struct {
		h    Hardware
		want [4]string
	}{
		{Hardware{"android", "arm64", "qualcomm", "snapdragon-8-gen-3", "unified", "12-16"}, [4]string{"qualcomm:snapdragon-8-gen-3:unified:12-16", "qualcomm:snapdragon-8-gen-3", "qualcomm:snapdragon-8:12-16", "opencl:12-16"}},
		{Hardware{"ios", "arm64", "apple", "a17-pro", "unified", "8-12"}, [4]string{"apple:a17-pro:unified:8-12", "apple:a17-pro", "apple:a-series:8-12", "opencl:8-12"}},
		{Hardware{"ipados", "arm64", "apple", "m4", "unified", "16-24"}, [4]string{"apple:m4:unified:16-24", "apple:m4", "apple:apple-silicon:16-24", "opencl:16-24"}},
	} {
		got := Cohorts(c.h, "opencl")
		if got != c.want {
			t.Errorf("cohorts %v, want %v", got, c.want)
		}
		back, err := ParseKey(got[0])
		if err != nil || back.Key() != got[0] || !IsPhone(back.Platform) {
			t.Errorf("parse %s: %+v %v", got[0], back, err)
		}
	}
	// A computer key still reads as a computer, and Apple's as a Mac.
	if h, err := ParseKey("apple:m4-max:unified:32-64"); err != nil || h.Platform != "macos" {
		t.Errorf("mac key %+v %v", h, err)
	}
	if h, err := ParseKey("nvidia:rtx-4090:dedicated:16-32"); err != nil || IsPhone(h.Platform) {
		t.Errorf("nvidia key %+v %v", h, err)
	}
	for _, k := range []string{"google:pixel-8:unified:8-12", "qualcomm:snapdragon-8-gen-3:unified:8-16", "nvidia:rtx-4090:dedicated:8-12"} {
		if _, err := ParseKey(k); err == nil {
			t.Errorf("%s parsed", k)
		}
	}
	for v, want := range map[[2]string]string{
		{"apple", "a18-pro"}: "a-series", {"apple", "a12z"}: "a-series", {"apple", "m2"}: "apple-silicon",
		{"qualcomm", "snapdragon-8-elite"}: "snapdragon-8", {"qualcomm", "snapdragon-8s-gen-3"}: "snapdragon-8",
		{"qualcomm", "snapdragon-888"}: "snapdragon-8", {"qualcomm", "snapdragon-778g"}: "snapdragon-7", {"qualcomm", "snapdragon-4-gen-2"}: "snapdragon-4",
		{"samsung", "exynos-2400"}: "exynos-2000", {"samsung", "exynos-990"}: "exynos-900",
		{"google", "tensor-g4"}: "tensor", {"google", "tensor"}: "tensor",
		{"mediatek", "dimensity-9300"}: "dimensity-9000", {"mediatek", "dimensity-700"}: "dimensity-700", {"mediatek", "helio-g99"}: "helio",
	} {
		if got := Class(v[0], v[1]); got != want {
			t.Errorf("Class(%s, %s) = %s, want %s", v[0], v[1], got, want)
		}
	}
}

// The published JSON schema lists the same values the service accepts.
func TestJSONSchemaMatches(t *testing.T) {
	b, err := os.ReadFile("../../schema/rating-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	type enum struct {
		Enum []string `json:"enum"`
	}
	var doc struct {
		Properties struct {
			Runtime struct {
				Properties struct{ Type, Backend enum } `json:"properties"`
			} `json:"runtime"`
			Hardware struct {
				Properties struct {
					Platform, Architecture, Vendor enum
					MemoryType                     enum `json:"memory_type"`
					MemoryBucket                   enum `json:"memory_bucket_gb"`
				} `json:"properties"`
			} `json:"hardware"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	rt, hw := doc.Properties.Runtime.Properties, doc.Properties.Hardware.Properties
	for name, pair := range map[string][2][]string{
		"runtime.type":     {rt.Type.Enum, Runtimes},
		"runtime.backend":  {rt.Backend.Enum, Backends},
		"platform":         {hw.Platform.Enum, Platforms},
		"architecture":     {hw.Architecture.Enum, Archs},
		"vendor":           {hw.Vendor.Enum, Vendors},
		"memory_type":      {hw.MemoryType.Enum, Memories},
		"memory_bucket_gb": {hw.MemoryBucket.Enum, Buckets},
	} {
		a, b := slices.Sorted(slices.Values(pair[0])), slices.Sorted(slices.Values(pair[1]))
		if !slices.Equal(a, b) {
			t.Errorf("%s: schema %v, service %v", name, a, b)
		}
	}
	if !strings.Contains(string(b), `"const": "llama.rn"`) {
		t.Error("the schema does not tie phones to llama.rn")
	}
}
