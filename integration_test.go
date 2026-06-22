package rewindrewind_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rr "rewindrewind.com/go"
)

// receivedPayload is a minimal mirror of the wire shape, for assertions.
type receivedPayload struct {
	Environment string            `json:"environment"`
	Platform    string            `json:"platform"`
	Level       string            `json:"level"`
	Message     string            `json:"message"`
	Timestamp   int64             `json:"timestamp"`
	Tags        map[string]string `json:"tags"`
	Exception   struct {
		Type       string `json:"type"`
		Value      string `json:"value"`
		Stacktrace []struct {
			Filename string `json:"filename"`
			Function string `json:"function"`
			Module   string `json:"module"`
			Line     int    `json:"line"`
			InApp    bool   `json:"in_app"`
		} `json:"stacktrace"`
	} `json:"exception"`
}

func newRecorder(t *testing.T) (*httptest.Server, *receivedPayload, *string) {
	t.Helper()
	var got receivedPayload
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true,"eventId":"evt_abc"}`))
	}))
	return srv, &got, &auth
}

// This test runs from an EXTERNAL package, faithfully simulating a real
// consumer: its frames must be in_app, the SDK's frames must be elided, and the
// first frame must be this application code (the culprit source).
func TestConsumerCapture(t *testing.T) {
	srv, got, auth := newRecorder(t)
	defer srv.Close()

	c := rr.New(rr.Config{
		Key:         "rrpub_test",
		Endpoint:    srv.URL,
		Environment: "test",
		Tags:        map[string]string{"service": "checkout"},
	})

	id := c.CaptureException(errors.New("kaboom"), rr.WithTag("op", "charge"))

	if id != "evt_abc" {
		t.Errorf("event id = %q, want evt_abc", id)
	}
	if *auth != "Bearer rrpub_test" {
		t.Errorf("auth = %q", *auth)
	}
	if got.Platform != "go" || got.Level != "error" || got.Message != "kaboom" {
		t.Errorf("payload basics wrong: %+v", got)
	}
	if got.Timestamp == 0 {
		t.Error("timestamp not set")
	}
	if got.Tags["service"] != "checkout" || got.Tags["op"] != "charge" {
		t.Errorf("tags not merged: %+v", got.Tags)
	}
	if len(got.Exception.Stacktrace) == 0 {
		t.Fatal("no frames")
	}

	first := got.Exception.Stacktrace[0]
	if !first.InApp {
		t.Errorf("first frame must be in_app (the culprit): %+v", first)
	}
	if !strings.HasSuffix(first.Filename, "integration_test.go") {
		t.Errorf("first frame should be this test file, got %q", first.Filename)
	}
	if !strings.Contains(first.Function, "TestConsumerCapture") {
		t.Errorf("first frame function = %q", first.Function)
	}
	for _, f := range got.Exception.Stacktrace {
		if strings.HasPrefix(f.Module, "rewindrewind.com/go") &&
			f.Module == "rewindrewind.com/go" {
			t.Errorf("SDK internal frame leaked: %+v", f)
		}
	}
}

func TestDisabledClientIsNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("disabled client should not send")
	}))
	defer srv.Close()
	c := rr.New(rr.Config{Key: "k", Endpoint: srv.URL, Environment: "test", Enabled: rr.Bool(false)})
	if id := c.CaptureException(errors.New("x")); id != "" {
		t.Errorf("disabled returned %q", id)
	}
}

func TestNilDefaultClientSafe(t *testing.T) {
	// Package-level helpers must tolerate Init never having been called.
	if id := rr.CaptureException(errors.New("x")); id != "" {
		t.Errorf("nil default returned %q", id)
	}
	rr.CaptureEvent("e", nil) // must not panic
}
