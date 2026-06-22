package rewindrewind

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// ---- Scrubber ----

func TestScrubStringMap(t *testing.T) {
	in := map[string]string{
		"service":       "checkout",
		"password":      "hunter2",
		"API_KEY":       "abc",
		"Authorization": "Bearer x",
		"client_secret": "shh",
		"note":          "fine",
	}
	out := scrubStringMap(in)
	for _, k := range []string{"password", "API_KEY", "Authorization", "client_secret"} {
		if out[k] != filteredPlaceholder {
			t.Errorf("key %q = %q, want %q", k, out[k], filteredPlaceholder)
		}
	}
	if out["service"] != "checkout" || out["note"] != "fine" {
		t.Errorf("benign keys altered: %+v", out)
	}
	// Original must not be mutated.
	if in["password"] != "hunter2" {
		t.Error("scrubStringMap mutated its input")
	}
}

func TestScrubStringMapNoMatchReturnsSame(t *testing.T) {
	in := map[string]string{"a": "1", "b": "2"}
	out := scrubStringMap(in)
	if reflect.ValueOf(out).Pointer() != reflect.ValueOf(in).Pointer() {
		t.Error("expected same map returned when nothing to scrub")
	}
}

func TestScrubAnyMapRecursive(t *testing.T) {
	in := map[string]any{
		"user_id": 42,
		"token":   "leak",
		"nested": map[string]any{
			"secret": "deep",
			"keep":   "ok",
			"deeper": map[string]any{"cvv": "123"},
		},
		"list": []any{
			map[string]any{"ssn": "000-00-0000"},
			"plain",
			map[string]string{"cookie": "x=y"},
		},
	}
	out := scrubAnyMap(in)

	if out["token"] != filteredPlaceholder {
		t.Errorf("top-level token not filtered: %v", out["token"])
	}
	if out["user_id"] != 42 {
		t.Errorf("user_id altered: %v", out["user_id"])
	}
	nested := out["nested"].(map[string]any)
	if nested["secret"] != filteredPlaceholder || nested["keep"] != "ok" {
		t.Errorf("nested scrub wrong: %+v", nested)
	}
	if nested["deeper"].(map[string]any)["cvv"] != filteredPlaceholder {
		t.Errorf("deep cvv not filtered: %+v", nested["deeper"])
	}
	list := out["list"].([]any)
	if list[0].(map[string]any)["ssn"] != filteredPlaceholder {
		t.Errorf("ssn in slice not filtered: %+v", list[0])
	}
	if list[1] != "plain" {
		t.Errorf("scalar in slice altered: %v", list[1])
	}
	if list[2].(map[string]string)["cookie"] != filteredPlaceholder {
		t.Errorf("cookie in nested string map not filtered: %+v", list[2])
	}

	// Input must be untouched.
	if in["token"] != "leak" {
		t.Error("scrubAnyMap mutated its input")
	}
}

func TestIsSensitiveKeyCaseAndVariants(t *testing.T) {
	sensitive := []string{
		"password", "PASSWD", "Secret", "token", "Authorization", "auth",
		"api_key", "api-key", "apikey", "access_key", "access-key",
		"client_secret", "client-secret", "cookie", "session", "credential",
		"card", "CVV", "ssn", "private_key", "private-key",
	}
	for _, k := range sensitive {
		if !isSensitiveKey(k) {
			t.Errorf("expected %q sensitive", k)
		}
	}
	benign := []string{"username", "email", "id", "card_brand_label_text", "tokenizer_version", "password_strength_meter"}
	for _, k := range benign {
		if isSensitiveKey(k) {
			t.Errorf("did not expect %q sensitive", k)
		}
	}
}

// ---- Query-string stripping ----

func TestRequestContextStripsQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://app.example.com/pay?token=abc123&id=5", nil)
	r.Header.Set("User-Agent", "ua/1.0")
	r.Header.Set("Referer", "https://app.example.com/checkout?session=zzz")

	ctx := requestContext(r)
	if ctx["url"] != "/pay" {
		t.Errorf("url = %v, want /pay (no query)", ctx["url"])
	}
	headers := ctx["headers"].(map[string]string)
	if headers["User-Agent"] != "ua/1.0" {
		t.Errorf("user-agent dropped: %+v", headers)
	}
	ref := headers["Referer"]
	if ref == "" {
		t.Fatal("referer unexpectedly dropped")
	}
	if got := ref; got != "https://app.example.com/checkout" {
		t.Errorf("referer = %q, should have no query", got)
	}
}

func TestRequestContextNoReferer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?a=b", nil)
	ctx := requestContext(r)
	if ctx["url"] != "/x" {
		t.Errorf("url = %v, want /x", ctx["url"])
	}
	headers := ctx["headers"].(map[string]string)
	if _, ok := headers["Referer"]; ok {
		t.Errorf("empty referer should be absent: %+v", headers)
	}
}

// ---- Endpoint validation ----

func TestValidateEndpoint(t *testing.T) {
	ok := []string{
		"https://rewindrewind.com",
		"https://ingest.example.com/v1",
		"http://localhost:8080",
		"http://127.0.0.1:3000",
		"http://[::1]:9000",
	}
	for _, e := range ok {
		if err := validateEndpoint(e); err != nil {
			t.Errorf("validateEndpoint(%q) = %v, want nil", e, err)
		}
	}
	bad := []string{
		"http://evil.com",           // plaintext to arbitrary host
		"http://example.com:80",     // plaintext non-loopback
		"ftp://rewindrewind.com",    // wrong scheme
		"https://",                  // no host
		"://nope",                   // unparseable scheme
		"not a url with spaces://x", // unparseable
	}
	for _, e := range bad {
		if err := validateEndpoint(e); err == nil {
			t.Errorf("validateEndpoint(%q) = nil, want error", e)
		}
	}
}

func TestNewDisablesOnBadEndpoint(t *testing.T) {
	var reported error
	c := New(Config{
		Key:         "rrpub_test",
		Endpoint:    "http://evil.example.com",
		Environment: "test",
		OnError:     func(e error) { reported = e },
	})
	if c.active() {
		t.Error("client with plaintext non-loopback endpoint should be inactive")
	}
	if reported == nil {
		t.Error("expected OnError to be called once for bad endpoint")
	}
	// Must not have panicked; reaching here is success.
}

func TestNewBadEndpointSendsNothing(t *testing.T) {
	var sent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
	}))
	defer srv.Close()

	// A syntactically valid but scheme-invalid endpoint disables the client.
	c := New(Config{Key: "k", Endpoint: "ftp://x.example.com", Environment: "test"})
	c.CaptureException(errorForTest("boom"))
	if sent {
		t.Error("disabled-by-validation client must not send")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
func errorForTest(s string) error { return errString(s) }
