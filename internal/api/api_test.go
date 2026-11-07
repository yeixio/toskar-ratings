package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/toskar-ratings/internal/aggregate"
	"github.com/yeixio/toskar-ratings/internal/store"
)

const secret = "0123456789abcdef0123456789abcdef"

func server(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"), []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &Server{Store: st, Secret: []byte(secret), AdminToken: "admin-token", Now: func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) }}
	return s, s.Handler()
}

func rating(client string, stars int, family, bucket string) string {
	return fmt.Sprintf(`{"schema_version":1,"client_id":%q,
	 "model":{"id":"qwen2.5-coder-7b-instruct","format":"gguf","quantization":"Q4_K_M"},
	 "runtime":{"type":"llamacpp","backend":"metal"},
	 "hardware":{"platform":"macos","architecture":"arm64","vendor":"apple","family":%q,"memory_type":"unified","memory_bucket_gb":%q},
	 "stars":%d,"tags":["fast"],"observations":{"tokens_per_second":%d,"starts":4,"start_failures":0}}`, client, family, bucket, stars, 20+stars)
}

func client(n int) string { return fmt.Sprintf("%032x", n+1) }

func do(t *testing.T, h http.Handler, method, path, body string, hdr map[string]string, from string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = from
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// The full flow: rate, rate again (an update), read by hardware tier, see
// the public snapshot, and delete.
func TestRatingsFlow(t *testing.T) {
	_, h := server(t)
	rr := do(t, h, "POST", "/v1/ratings", rating(client(0), 3, "m4-max", "32-64"), nil, "203.0.113.1:5000")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rr.Code, rr.Body)
	}
	var put struct {
		Key     string `json:"key"`
		Created bool   `json:"created"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &put)
	// The same client rating the same configuration updates it.
	rr = do(t, h, "POST", "/v1/ratings", rating(client(0), 5, "m4-max", "32-64"), nil, "203.0.113.1:5000")
	var again struct {
		Key     string `json:"key"`
		Created bool   `json:"created"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &again)
	if rr.Code != http.StatusOK || again.Created || again.Key != put.Key {
		t.Fatalf("second rating %d %s", rr.Code, rr.Body)
	}
	// Others with the same and with similar hardware.
	for i := 1; i <= 4; i++ {
		do(t, h, "POST", "/v1/ratings", rating(client(i), 4, "m4-max", "32-64"), nil, fmt.Sprintf("198.51.100.%d:1", i))
	}
	for i := 5; i <= 7; i++ {
		do(t, h, "POST", "/v1/ratings", rating(client(i), 2, "m3-pro", "32-64"), nil, fmt.Sprintf("198.51.%d.1:1", i))
	}
	do(t, h, "POST", "/v1/ratings", rating(client(8), 1, "m1", "8-16"), nil, "192.0.2.9:1")

	rr = do(t, h, "GET", "/v1/models/qwen2.5-coder-7b-instruct/ratings?format=gguf&quantization=Q4_K_M&runtime=llamacpp&backend=metal&hardware=apple:m4-max:unified:32-64", "", nil, "192.0.2.10:1")
	var resp modelResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || rr.Code != 200 {
		t.Fatalf("query %d %s", rr.Code, rr.Body)
	}
	got := map[string]aggregate.Stats{}
	for _, s := range resp.Tiers {
		got[s.Tier] = s
	}
	if got["exact"].Ratings != 5 || got["exact"].Average != 4.2 || got["class"].Ratings != 8 || got["global"].Ratings != 9 {
		t.Fatalf("tiers %+v", resp.Tiers)
	}
	if got["exact"].Confidence != aggregate.Early || got["exact"].Tags["fast"] != 5 || *got["exact"].MedianTokensPerSecond != 24 || *got["exact"].SuccessfulStartRate != 1 {
		t.Fatalf("exact %+v", got["exact"])
	}
	// The weighted score leans toward the prior with few ratings.
	if w := got["exact"].WeightedScore; w <= 3.5 || w >= 4.2 {
		t.Fatalf("weighted %v", w)
	}

	// The public snapshot leaves out cohorts with fewer than three ratings
	// and every exact cohort.
	rr = do(t, h, "GET", "/v1/aggregates", "", nil, "192.0.2.10:1")
	var snap aggregate.Snapshot
	_ = json.Unmarshal(rr.Body.Bytes(), &snap)
	if len(snap.Models) != 1 || snap.SchemaVersion != 1 || snap.MinRatings != 3 {
		t.Fatalf("snapshot %s", rr.Body)
	}
	for _, c := range snap.Models[0].Cohorts {
		if c.Tier == "exact" || c.Ratings < 3 || strings.Contains(c.Cohort, "m1") {
			t.Errorf("published cohort %+v", c)
		}
	}
	if bytes.Contains(rr.Body.Bytes(), []byte(client(0))) || bytes.Contains(rr.Body.Bytes(), []byte("203.0.113")) {
		t.Fatal("the snapshot names a client or an address")
	}

	// Only the owner can change or delete a rating.
	upd := fmt.Sprintf(`{"client_id":%q,"stars":2}`, client(1))
	if rr := do(t, h, "PUT", "/v1/ratings/"+put.Key, upd, nil, "192.0.2.11:1"); rr.Code != http.StatusForbidden {
		t.Fatalf("update by another client %d", rr.Code)
	}
	upd = fmt.Sprintf(`{"client_id":%q,"stars":2,"tags":["slow"]}`, client(0))
	if rr := do(t, h, "PUT", "/v1/ratings/"+put.Key, upd, nil, "192.0.2.11:1"); rr.Code != http.StatusOK {
		t.Fatalf("update %d %s", rr.Code, rr.Body)
	}
	if rr := do(t, h, "DELETE", "/v1/ratings/"+put.Key, "", map[string]string{"X-Ratings-Client": client(2)}, "192.0.2.11:1"); rr.Code != http.StatusForbidden {
		t.Fatalf("delete by another client %d", rr.Code)
	}
	if rr := do(t, h, "DELETE", "/v1/ratings/"+put.Key, "", map[string]string{"X-Ratings-Client": client(0)}, "192.0.2.11:1"); rr.Code != http.StatusNoContent {
		t.Fatalf("delete %d", rr.Code)
	}
	if rr := do(t, h, "DELETE", "/v1/ratings/"+put.Key, "", map[string]string{"X-Ratings-Client": client(0)}, "192.0.2.11:1"); rr.Code != http.StatusNotFound {
		t.Fatalf("delete again %d", rr.Code)
	}
}

func TestRejectsBadInput(t *testing.T) {
	_, h := server(t)
	for _, body := range []string{
		`{}`, `not json`,
		strings.Replace(rating(client(0), 4, "m4-max", "32-64"), `"stars":4`, `"stars":9`, 1),
		strings.Replace(rating(client(0), 4, "m4-max", "32-64"), `"stars":4`, `"stars":4,"hostname":"my-laptop"`, 1),
	} {
		if rr := do(t, h, "POST", "/v1/ratings", body, nil, "203.0.113.1:1"); rr.Code < 400 || rr.Code >= 500 {
			t.Errorf("%.40q accepted: %d", body, rr.Code)
		}
	}
	if rr := do(t, h, "GET", "/v1/models/x/ratings?format=gguf", "", nil, "203.0.113.1:1"); rr.Code != http.StatusBadRequest {
		t.Errorf("incomplete query %d", rr.Code)
	}
}

// Writes are limited per network, and a flood can be invalidated by batch.
func TestRateLimitAndInvalidate(t *testing.T) {
	s, _ := server(t)
	s.writes = newLimiter(3, time.Hour)
	h := s.Handler()
	for i := 0; i < 3; i++ {
		if rr := do(t, h, "POST", "/v1/ratings", rating(client(i), 5, "m4-max", "32-64"), nil, fmt.Sprintf("203.0.113.%d:1", i+1)); rr.Code != http.StatusCreated {
			t.Fatalf("write %d: %d", i, rr.Code)
		}
	}
	// Same /24: limited.
	if rr := do(t, h, "POST", "/v1/ratings", rating(client(9), 5, "m4-max", "32-64"), nil, "203.0.113.200:1"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit %d", rr.Code)
	}
	if rr := do(t, h, "GET", "/v1/admin/batches", "", nil, "203.0.113.1:1"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("admin without token %d", rr.Code)
	}
	auth := map[string]string{"Authorization": "Bearer admin-token"}
	rr := do(t, h, "GET", "/v1/admin/batches", "", auth, "203.0.113.1:1")
	var batches []store.Batch
	_ = json.Unmarshal(rr.Body.Bytes(), &batches)
	if len(batches) != 1 || batches[0].Ratings != 3 || !strings.HasPrefix(batches[0].Batch, "2026100212-") {
		t.Fatalf("batches %s", rr.Body)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("203.0.113")) {
		t.Fatal("a batch names an address")
	}
	rr = do(t, h, "POST", "/v1/admin/invalidate", fmt.Sprintf(`{"batch":%q}`, batches[0].Batch), auth, "203.0.113.1:1")
	if !strings.Contains(rr.Body.String(), `"invalidated":3`) {
		t.Fatalf("invalidate %s", rr.Body)
	}
	rr = do(t, h, "GET", "/v1/models/qwen2.5-coder-7b-instruct/ratings?format=gguf&quantization=Q4_K_M&runtime=llamacpp&backend=metal", "", nil, "192.0.2.1:1")
	if !strings.Contains(rr.Body.String(), `"tiers":[]`) {
		t.Fatalf("invalidated ratings still count: %s", rr.Body)
	}
}

func TestPrior(t *testing.T) {
	if aggregate.Prior(4.8, 10) != aggregate.DefaultPrior || aggregate.Prior(4.1, 500) != 4.1 {
		t.Fatal("prior")
	}
	// One 5-star rating does not outrank 400 ratings averaging 4.8.
	one := aggregate.Summarize(4, "", []store.Row{{Stars: 5}}, 3.5)
	many := make([]store.Row, 400)
	for i := range many {
		many[i].Stars = 5
		if i%5 == 0 {
			many[i].Stars = 4
		}
	}
	if aggregate.Summarize(4, "", many, 3.5).WeightedScore <= one.WeightedScore {
		t.Fatal("a single rating outranked many")
	}
}

// A rating can say the language the model was used in. The snapshot shows
// each language with enough ratings, for the configuration as a whole.
func TestLanguages(t *testing.T) {
	_, h := server(t)
	withLanguage := func(n, stars int, lang string) {
		t.Helper()
		body := strings.Replace(rating(client(n), stars, "m4-max", "32-64"), `"stars":`, `"language":"`+lang+`","stars":`, 1)
		if rr := do(t, h, "POST", "/v1/ratings", body, nil, fmt.Sprintf("198.51.100.%d:1", n+1)); rr.Code != http.StatusCreated {
			t.Fatalf("rating in %s: %d %s", lang, rr.Code, rr.Body)
		}
	}
	for i := 0; i < 3; i++ {
		withLanguage(i, 2, "es")
	}
	withLanguage(3, 5, "fr")
	withLanguage(4, 5, "zh-Hant")
	do(t, h, "POST", "/v1/ratings", rating(client(5), 5, "m4-max", "32-64"), nil, "198.51.100.9:1")

	for _, bad := range []string{"EN", "en-US", "english", "zh-Latn", "e"} {
		body := strings.Replace(rating(client(9), 4, "m4-max", "32-64"), `"stars":`, `"language":"`+bad+`","stars":`, 1)
		if rr := do(t, h, "POST", "/v1/ratings", body, nil, "192.0.2.1:1"); rr.Code < 400 || rr.Code >= 500 {
			t.Errorf("language %q accepted: %d", bad, rr.Code)
		}
	}

	snapshot := func() []aggregate.LanguageStats {
		rr := do(t, h, "GET", "/v1/aggregates", "", nil, "192.0.2.10:1")
		var snap aggregate.Snapshot
		if err := json.Unmarshal(rr.Body.Bytes(), &snap); err != nil || len(snap.Models) != 1 {
			t.Fatalf("snapshot %s", rr.Body)
		}
		return snap.Models[0].Languages
	}
	// French and Chinese have too few ratings to show.
	if got := snapshot(); len(got) != 1 || got[0].Language != "es" || got[0].Ratings != 3 || got[0].Average != 2 {
		t.Fatalf("languages %+v", got)
	}

	// Changing a rating can add its language.
	rr := do(t, h, "POST", "/v1/ratings", rating(client(6), 4, "m4-max", "32-64"), nil, "198.51.100.10:1")
	var put struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &put)
	upd := fmt.Sprintf(`{"client_id":%q,"stars":4,"language":"fr"}`, client(6))
	if rr := do(t, h, "PUT", "/v1/ratings/"+put.Key, upd, nil, "192.0.2.11:1"); rr.Code != http.StatusOK {
		t.Fatalf("update %d %s", rr.Code, rr.Body)
	}
	withLanguage(7, 3, "fr")
	got := snapshot()
	if len(got) != 2 || got[1].Language != "fr" || got[1].Ratings != 3 || got[1].Average != 4 {
		t.Fatalf("languages after update %+v", got)
	}
	upd = fmt.Sprintf(`{"client_id":%q,"stars":4,"language":"French"}`, client(6))
	if rr := do(t, h, "PUT", "/v1/ratings/"+put.Key, upd, nil, "192.0.2.11:1"); rr.Code < 400 || rr.Code >= 500 {
		t.Fatalf("bad language on update %d", rr.Code)
	}
}

func phoneRating(cl string, stars int, platform, vendor, family, backend, bucket string) string {
	return fmt.Sprintf(`{"schema_version":1,"client_id":%q,
	 "model":{"id":"qwen2.5-coder-7b-instruct","format":"gguf","quantization":"Q4_K_M"},
	 "runtime":{"type":"llama.rn","backend":%q,"version":"0.6.3"},
	 "hardware":{"platform":%q,"architecture":"arm64","vendor":%q,"family":%q,"memory_type":"unified","memory_bucket_gb":%q},
	 "stars":%d,"app_version":"1.12.1","observations":{"tokens_per_second":%d}}`, cl, backend, platform, vendor, family, bucket, stars, 5+stars)
}

// Phones and tablets rate the same GGUF as computers; their cohorts stay
// apart, and the snapshot publishes them with the same minimum.
func TestPhoneRatings(t *testing.T) {
	_, h := server(t)
	post := func(body, from string) {
		t.Helper()
		if rr := do(t, h, "POST", "/v1/ratings", body, nil, from); rr.Code != http.StatusCreated {
			t.Fatalf("rating %d %s", rr.Code, rr.Body)
		}
	}
	// Three Macs, three Snapdragon 8 phones (two chips), three iPhones, and
	// one Pixel: all behind one carrier NAT address but the Macs.
	for i := 0; i < 3; i++ {
		post(rating(client(i), 4, "m4-max", "32-64"), fmt.Sprintf("198.51.100.%d:1", i+1))
	}
	nat := "203.0.113.7:1"
	post(phoneRating(client(10), 3, "android", "qualcomm", "snapdragon-8-gen-3", "opencl", "12-16"), nat)
	post(phoneRating(client(11), 3, "android", "qualcomm", "snapdragon-8-gen-3", "opencl", "12-16"), nat)
	post(phoneRating(client(12), 2, "android", "qualcomm", "snapdragon-8-elite", "opencl", "12-16"), nat)
	for i := 20; i < 23; i++ {
		post(phoneRating(client(i), 5, "ios", "apple", "a17-pro", "metal", "8-12"), nat)
	}
	post(phoneRating(client(30), 1, "android", "google", "tensor-g3", "cpu", "8-12"), nat)

	// A computer asking about its hardware sees only computers.
	rr := do(t, h, "GET", "/v1/models/qwen2.5-coder-7b-instruct/ratings?format=gguf&quantization=Q4_K_M&runtime=llamacpp&backend=metal&hardware=apple:m4-max:unified:32-64", "", nil, "192.0.2.10:1")
	var resp modelResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if n := len(resp.Tiers); n == 0 || resp.Tiers[n-1].Tier != "global" || resp.Tiers[n-1].Ratings != 3 {
		t.Fatalf("computer tiers %d %s", rr.Code, rr.Body)
	}
	// A phone asks with its own exact key, and sees only phones like it.
	rr = do(t, h, "GET", "/v1/models/qwen2.5-coder-7b-instruct/ratings?format=gguf&quantization=Q4_K_M&runtime=llama.rn&backend=opencl&hardware=qualcomm:snapdragon-8-gen-3:unified:12-16", "", nil, "192.0.2.10:1")
	resp = modelResponse{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("phone query %d %s", rr.Code, rr.Body)
	}
	got := map[string]aggregate.Stats{}
	for _, st := range resp.Tiers {
		got[st.Tier] = st
	}
	if got["exact"].Ratings != 2 || got["class"].Ratings != 3 || got["class"].Cohort != "qualcomm:snapdragon-8:12-16" || got["global"].Ratings != 3 {
		t.Fatalf("phone tiers %+v", resp.Tiers)
	}

	rr = do(t, h, "GET", "/v1/aggregates", "", nil, "192.0.2.10:1")
	var snap aggregate.Snapshot
	_ = json.Unmarshal(rr.Body.Bytes(), &snap)
	published := map[string]map[string]int{}
	for _, m := range snap.Models {
		cs := map[string]int{}
		for _, c := range m.Cohorts {
			if c.Tier == "exact" || c.Ratings < aggregate.MinPublic {
				t.Errorf("published %+v", c)
			}
			cs[c.Tier+" "+c.Cohort] = c.Ratings
		}
		published[m.Runtime+"/"+m.Backend] = cs
	}
	want := map[string]map[string]int{
		"llamacpp/metal":  {"family apple:m4-max": 3, "class apple:apple-silicon:32-64": 3, "backend metal:32-64": 3, "global ": 3},
		"llama.rn/opencl": {"class qualcomm:snapdragon-8:12-16": 3, "backend opencl:12-16": 3, "global ": 3},
		"llama.rn/metal":  {"family apple:a17-pro": 3, "class apple:a-series:8-12": 3, "backend metal:8-12": 3, "global ": 3},
		// llama.rn/cpu has one rating, so it is not published at all.
	}
	if fmt.Sprint(published) != fmt.Sprint(want) {
		t.Fatalf("published\n%v\nwant\n%v", published, want)
	}
	// A phone key with a computer runtime, or the reverse, is not a query.
	for _, q := range []string{
		"runtime=llamacpp&backend=metal&hardware=apple:a17-pro:unified:8-12",
		"runtime=llama.rn&backend=metal&hardware=apple:m4-max:unified:32-64",
		"runtime=llama.rn&backend=cuda",
	} {
		if rr := do(t, h, "GET", "/v1/models/qwen2.5-coder-7b-instruct/ratings?format=gguf&quantization=Q4_K_M&"+q, "", nil, "192.0.2.10:1"); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rr.Code)
		}
	}
}

// A phone rating that carries a device model name, anywhere, is refused,
// so the name is never stored or published.
func TestPhoneDeviceNameRefused(t *testing.T) {
	s, h := server(t)
	ok := phoneRating(client(0), 4, "android", "google", "tensor-g3", "cpu", "8-12")
	for name, body := range map[string]string{
		"family":       phoneRating(client(0), 4, "android", "google", "pixel-8", "cpu", "8-12"),
		"samsung code": phoneRating(client(0), 4, "android", "samsung", "sm-s928b", "cpu", "8-12"),
		"iphone":       phoneRating(client(0), 4, "ios", "apple", "iphone16-2", "metal", "8-12"),
		"field":        strings.Replace(ok, `"memory_type"`, `"device_model":"Pixel 8","memory_type"`, 1),
		"top field":    strings.Replace(ok, `"stars"`, `"device":"Pixel 8","stars"`, 1),
		"app version":  strings.Replace(ok, `"app_version":"1.12.1"`, `"app_version":"Pixel-8"`, 1),
		"rn version":   strings.Replace(ok, `"version":"0.6.3"`, `"version":"SM-S928B"`, 1),
	} {
		if rr := do(t, h, "POST", "/v1/ratings", body, nil, "203.0.113.1:1"); rr.Code < 400 || rr.Code >= 500 {
			t.Errorf("%s accepted: %d", name, rr.Code)
		}
	}
	rows, err := s.Store.All(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("stored %d rows %v", len(rows), err)
	}
}

// Writes are limited per client_id, so one phone cannot use up its carrier
// NAT's allowance; the per-network limit stays as a backstop.
func TestClientLimitBehindNAT(t *testing.T) {
	s, _ := server(t)
	s.clientWrites = newLimiter(2, time.Hour)
	s.writes = newLimiter(5, time.Hour)
	h := s.Handler()
	nat := "203.0.113.7:1"
	post := func(cl string, stars int) int {
		return do(t, h, "POST", "/v1/ratings", phoneRating(cl, stars, "android", "qualcomm", "snapdragon-8-gen-3", "opencl", "12-16"), nil, nat).Code
	}
	if post(client(0), 4) != http.StatusCreated || post(client(0), 5) != http.StatusOK {
		t.Fatal("first writes refused")
	}
	// The same phone again, and changing or deleting, count against it.
	if c := post(client(0), 3); c != http.StatusTooManyRequests {
		t.Fatalf("third write by one client %d", c)
	}
	if rr := do(t, h, "DELETE", "/v1/ratings/x", "", map[string]string{"X-Ratings-Client": client(0)}, "192.0.2.1:1"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("delete over the client limit %d", rr.Code)
	}
	// Another phone behind the same address is not starved.
	if c := post(client(1), 4); c != http.StatusCreated {
		t.Fatalf("second phone %d", c)
	}
	if c := post(client(2), 4); c != http.StatusCreated {
		t.Fatalf("third phone %d", c)
	}
	// The network backstop: 5 writes from the address, counting refused ones.
	if c := post(client(3), 4); c != http.StatusTooManyRequests {
		t.Fatalf("over the network limit %d", c)
	}
}
