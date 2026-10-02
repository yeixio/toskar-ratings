package schema

import (
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
