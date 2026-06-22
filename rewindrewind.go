// Package rewindrewind is the official Go SDK for RewindRewind, an
// observability service for capturing exceptions and product events.
//
// Quick start:
//
//	rewindrewind.Init(rewindrewind.Config{
//		Key:         os.Getenv("REWINDREWIND_PROJECT_KEY"),
//		Environment: "production",
//		Release:     "v1.2.3",
//	})
//
//	if err := doWork(); err != nil {
//		rewindrewind.CaptureException(err)
//	}
//
// The endpoint defaults to https://rewindrewind.com and can be overridden with
// the REWINDREWIND_ENDPOINT environment variable or Config.Endpoint.
//
// The SDK depends only on the standard library (net/http, encoding/json,
// runtime). Capture calls never panic the caller: any transport or encoding
// error is swallowed and, when configured, surfaced through Config.OnError.
package rewindrewind

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultEndpoint is the production RewindRewind ingestion origin. Override it
// per-client via Config.Endpoint or globally via the REWINDREWIND_ENDPOINT
// environment variable.
const DefaultEndpoint = "https://rewindrewind.com"

// defaultTimeout bounds a single capture request. Observability should never
// block application work for long, so this is deliberately short.
const defaultTimeout = 2 * time.Second

// platform is the value reported in the "platform" field of every payload.
const platform = "go"

// maxEnvironmentLen mirrors the server-side limit (≤64 chars).
const maxEnvironmentLen = 64

// Config configures a Client. The zero value is not useful on its own — at a
// minimum Key and Environment should be set.
type Config struct {
	// Key is the project public ingestion key (rrpub_…). Required. It is sent
	// as a Bearer token and is safe to embed in client binaries.
	Key string

	// Endpoint overrides the ingestion origin. When empty, the SDK falls back
	// to the REWINDREWIND_ENDPOINT environment variable and then DefaultEndpoint.
	Endpoint string

	// Environment labels every payload (e.g. "production", "staging"). Required
	// by the server: it must be a non-empty string of at most 64 characters.
	Environment string

	// Release optionally identifies the deployed version (e.g. a git SHA or
	// semver tag) for every payload.
	Release string

	// Tags are merged into every exception's tags and every event's properties.
	// Per-call tags take precedence over these defaults.
	Tags map[string]string

	// Timeout bounds each capture request. Defaults to 2s when zero.
	Timeout time.Duration

	// Enabled, when set to a non-nil false, disables all capture (calls become
	// no-ops). A nil pointer means enabled. Use Disabled() for a false pointer.
	Enabled *bool

	// HTTPClient lets callers supply a custom transport (proxies, mocks,
	// instrumentation). When nil a client with Timeout is created.
	HTTPClient *http.Client

	// OnError receives non-fatal transport/encoding errors. It is optional and
	// intended for debugging; capture never returns an error to the caller.
	OnError func(error)
}

// Bool returns a pointer to v, a convenience for setting Config.Enabled
// (e.g. Config{Enabled: rewindrewind.Bool(false)}).
func Bool(v bool) *bool { return &v }

// Client sends exceptions and events to RewindRewind. A Client is safe for
// concurrent use by multiple goroutines.
type Client struct {
	key         string
	endpoint    string
	environment string
	release     string
	tags        map[string]string
	enabled     bool
	httpClient  *http.Client
	onError     func(error)
}

// New builds a Client from cfg, resolving the endpoint and timeout defaults.
func New(cfg Config) *Client {
	endpoint := strings.TrimRight(firstNonEmpty(cfg.Endpoint, os.Getenv("REWINDREWIND_ENDPOINT"), DefaultEndpoint), "/")

	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}

	// Validate the endpoint before we ever attach the Bearer key to a request.
	// An invalid or plaintext-to-arbitrary-host endpoint disables the client so
	// the key is never sent in the clear. The failure is reported once via
	// OnError rather than panicking the caller.
	if enabled {
		if err := validateEndpoint(endpoint); err != nil {
			enabled = false
			if cfg.OnError != nil {
				cfg.OnError(err)
			}
		}
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		httpClient = &http.Client{Timeout: timeout}
	}

	// Copy tags so later mutations of the caller's map don't leak in.
	var tags map[string]string
	if len(cfg.Tags) > 0 {
		tags = make(map[string]string, len(cfg.Tags))
		for k, v := range cfg.Tags {
			tags[k] = v
		}
	}

	return &Client{
		key:         strings.TrimSpace(cfg.Key),
		endpoint:    endpoint,
		environment: clampEnvironment(cfg.Environment),
		release:     cfg.Release,
		tags:        tags,
		enabled:     enabled,
		httpClient:  httpClient,
		onError:     cfg.OnError,
	}
}

// active reports whether the client should actually send. A client with no key
// is treated as disabled so a misconfigured app degrades to a silent no-op
// rather than spraying 401s.
func (c *Client) active() bool {
	return c != nil && c.enabled && c.key != ""
}

// reportError forwards a non-fatal error to the configured OnError hook.
func (c *Client) reportError(err error) {
	if err != nil && c.onError != nil {
		c.onError(err)
	}
}

var (
	defaultClient   *Client
	defaultClientMu sync.RWMutex
)

// Init configures the package-level default Client used by the package-level
// CaptureException / CaptureEvent / Recover helpers. It is safe to call once at
// program startup.
func Init(cfg Config) *Client {
	c := New(cfg)
	defaultClientMu.Lock()
	defaultClient = c
	defaultClientMu.Unlock()
	return c
}

// Default returns the package-level Client configured by Init, or nil if Init
// has not been called. The package-level capture helpers tolerate a nil default.
func Default() *Client {
	defaultClientMu.RLock()
	defer defaultClientMu.RUnlock()
	return defaultClient
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// validateEndpoint enforces that the ingestion endpoint is safe to send the
// Bearer key to: it must parse, have a non-empty host, and use https. Plain
// http is allowed only when the host is loopback (localhost/127.0.0.1/::1),
// which is where local development and the test suite run.
func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("rewindrewind: invalid endpoint %q: %w", endpoint, err)
	}
	if u.Host == "" {
		return fmt.Errorf("rewindrewind: endpoint %q has no host", endpoint)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("rewindrewind: refusing http endpoint %q to non-loopback host; use https", endpoint)
	default:
		return fmt.Errorf("rewindrewind: endpoint %q must use https (got scheme %q)", endpoint, u.Scheme)
	}
}

// isLoopbackHost reports whether host is a loopback name or address. The literal
// "localhost" is matched by name; everything else is parsed as an IP.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func clampEnvironment(env string) string {
	env = strings.TrimSpace(env)
	if len(env) > maxEnvironmentLen {
		env = env[:maxEnvironmentLen]
	}
	return env
}
