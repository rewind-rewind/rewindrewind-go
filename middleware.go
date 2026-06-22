package rewindrewind

import (
	"fmt"
	"net/http"
	"net/url"
)

// recoveredError wraps a value recovered from panic() so it satisfies error.
// When the panic value already is an error, that error is preserved (and its
// type reported) instead.
type recoveredError struct {
	value any
}

func (e *recoveredError) Error() string {
	return fmt.Sprintf("%v", e.value)
}

// asError normalizes a recovered panic value into an error, preserving an
// underlying error when present.
func asError(recovered any) error {
	if recovered == nil {
		return nil
	}
	if err, ok := recovered.(error); ok {
		return err
	}
	return &recoveredError{value: recovered}
}

// Middleware returns an http.Handler that wraps next, reporting any panic in the
// downstream handler to RewindRewind before responding with 500. The original
// panic is NOT re-propagated (the request is contained), matching the behavior
// expected of an HTTP recovery middleware. Use the client method variant to
// attach request context.
func (c *Client) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// Capture inside the deferred func so the stack still includes
				// the panicking frames; skip this deferred wrapper itself.
				c.CaptureExceptionContext(r.Context(), asError(rec),
					WithSkip(2),
					WithRequest(requestContext(r)),
				)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("Internal Server Error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Middleware wraps next using the default client configured by Init.
func Middleware(next http.Handler) http.Handler {
	return Default().Middleware(next)
}

// requestContext extracts a safe, lightweight summary of an HTTP request for
// the exception payload's "request" field. Bodies and most headers are omitted
// to avoid leaking sensitive data.
//
// Query strings are deliberately dropped: they routinely carry credentials
// (?token=…, ?api_key=…). Only the URL path is reported, and the Referer header
// is reduced to its path for the same reason. User-Agent is preserved.
func requestContext(r *http.Request) map[string]any {
	if r == nil {
		return nil
	}
	headers := map[string]string{
		"User-Agent": r.UserAgent(),
	}
	if ref := refererPath(r.Referer()); ref != "" {
		headers["Referer"] = ref
	}
	return map[string]any{
		"method":  r.Method,
		"url":     r.URL.Path,
		"headers": headers,
	}
}

// refererPath reduces a Referer header to scheme://host/path, dropping any
// query string (which can carry tokens). An unparseable value is dropped
// entirely rather than risk leaking a raw query.
func refererPath(referer string) string {
	if referer == "" {
		return ""
	}
	u, err := url.Parse(referer)
	if err != nil {
		return ""
	}
	if u.Host == "" {
		return u.Path
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// Recover reports a panic in flight and re-panics so the program's normal panic
// handling proceeds. Use it as a deferred guard in goroutines or main:
//
//	defer client.Recover()
//
// Because it re-panics, the process still crashes on a fatal panic — the report
// is delivered first. For panics you want to swallow, use RecoverSilent.
func (c *Client) Recover() {
	if rec := recover(); rec != nil {
		c.CaptureException(asError(rec), WithSkip(2))
		panic(rec)
	}
}

// Recover uses the default client.
func Recover() { Default().Recover() }

// RecoverSilent reports a panic in flight and swallows it, allowing the
// goroutine to return normally. Use it for background workers that should not
// take the process down:
//
//	go func() {
//		defer client.RecoverSilent()
//		doBackgroundWork()
//	}()
func (c *Client) RecoverSilent() {
	if rec := recover(); rec != nil {
		c.CaptureException(asError(rec), WithSkip(2))
	}
}

// RecoverSilent uses the default client.
func RecoverSilent() { Default().RecoverSilent() }
