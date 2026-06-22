package rewindrewind

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"
)

// exceptionPayload is the wire shape POSTed to /v1/exceptions.
type exceptionPayload struct {
	Timestamp   int64             `json:"timestamp,omitempty"`
	Environment string            `json:"environment"`
	Release     string            `json:"release,omitempty"`
	Platform    string            `json:"platform"`
	Level       string            `json:"level"`
	Message     string            `json:"message"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Exception   exceptionDetail   `json:"exception"`
	Request     map[string]any    `json:"request,omitempty"`
	User        *User             `json:"user,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Extra       map[string]any    `json:"extra,omitempty"`
}

type exceptionDetail struct {
	Type       string  `json:"type"`
	Value      string  `json:"value"`
	Stacktrace []Frame `json:"stacktrace"`
}

// eventPayload is the wire shape POSTed to /v1/events.
type eventPayload struct {
	Type        string         `json:"type"`
	Environment string         `json:"environment"`
	Release     string         `json:"release,omitempty"`
	DistinctID  string         `json:"distinct_id,omitempty"`
	AnonymousID string         `json:"anonymous_id,omitempty"`
	Source      string         `json:"source,omitempty"`
	Properties  map[string]any `json:"properties,omitempty"`
}

// CaptureException reports err to RewindRewind and returns its event ID (the
// server-assigned identifier), or an empty string when the client is disabled
// or the send fails. It never returns an error and never panics the caller;
// non-fatal failures are routed to Config.OnError.
//
// The stack trace is captured at the call site via runtime.Callers, so call
// CaptureException as close as possible to where the error surfaced.
func (c *Client) CaptureException(err error, opts ...Option) string {
	return c.CaptureExceptionContext(context.Background(), err, opts...)
}

// CaptureExceptionContext is the context-aware variant of CaptureException. The
// context bounds the HTTP request (in addition to the client's Timeout).
func (c *Client) CaptureExceptionContext(ctx context.Context, err error, opts ...Option) string {
	if !c.active() || err == nil {
		return ""
	}

	o := newCaptureOptions(opts)

	message := o.message
	if message == "" {
		message = err.Error()
	}

	payload := exceptionPayload{
		Timestamp:   time.Now().UnixMilli(),
		Environment: c.environment,
		Release:     c.release,
		Platform:    platform,
		Level:       o.level,
		Message:     message,
		Fingerprint: o.fingerprint,
		Exception: exceptionDetail{
			Type:  errorType(err),
			Value: err.Error(),
			// +1 to skip CaptureExceptionContext itself.
			Stacktrace: captureStack(o.skip + 1),
		},
		Request: scrubAnyMap(o.request),
		User:    o.user,
		Tags:    scrubStringMap(c.mergeTags(o.tags)),
		Extra:   scrubAnyMap(o.extra),
	}

	var result struct {
		EventID string `json:"eventId"`
		ID      string `json:"id"`
	}
	if err := c.post(ctx, "/v1/exceptions", payload, &result); err != nil {
		c.reportError(err)
		return ""
	}
	return firstNonEmpty(result.EventID, result.ID)
}

// CaptureEvent records a product/analytics event. eventType is required.
// Returns nothing and never panics; failures go to Config.OnError.
func (c *Client) CaptureEvent(eventType string, props map[string]any) {
	c.CaptureEventContext(context.Background(), eventType, props)
}

// CaptureEventContext is the context-aware variant of CaptureEvent.
func (c *Client) CaptureEventContext(ctx context.Context, eventType string, props map[string]any) {
	if !c.active() || eventType == "" {
		return
	}
	payload := eventPayload{
		Type:        eventType,
		Environment: c.environment,
		Release:     c.release,
		Source:      platform,
		Properties:  scrubAnyMap(c.mergeProperties(props)),
	}
	if err := c.post(ctx, "/v1/events", payload, nil); err != nil {
		c.reportError(err)
	}
}

// mergeTags layers per-call tags over the client's default tags, returning nil
// when there are none so the field is omitted from the payload.
func (c *Client) mergeTags(callTags map[string]string) map[string]string {
	if len(c.tags) == 0 && len(callTags) == 0 {
		return nil
	}
	merged := make(map[string]string, len(c.tags)+len(callTags))
	for k, v := range c.tags {
		merged[k] = v
	}
	for k, v := range callTags {
		merged[k] = v
	}
	return merged
}

// mergeProperties layers the client's default tags under the event's explicit
// properties (properties win on conflict).
func (c *Client) mergeProperties(props map[string]any) map[string]any {
	if len(c.tags) == 0 && len(props) == 0 {
		return nil
	}
	merged := make(map[string]any, len(c.tags)+len(props))
	for k, v := range c.tags {
		merged[k] = v
	}
	for k, v := range props {
		merged[k] = v
	}
	return merged
}

// post encodes body as JSON and POSTs it to path. Any 2xx is success. When out
// is non-nil and the response has a JSON body, it is decoded into out. Errors
// are returned for the caller (capture methods) to route to OnError.
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("rewindrewind: encode %s: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("rewindrewind: build request %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "rewindrewind-go")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("rewindrewind: send %s: %w", path, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("rewindrewind: %s returned %d: %s", path, resp.StatusCode, bytes.TrimSpace(snippet))
	}

	if out != nil {
		// Best-effort decode; a missing/empty body is not an error.
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// errorType derives a stable type name for err. It prefers a concrete struct's
// package-qualified type (e.g. "*url.Error") and falls back to "error" for
// dynamically constructed errors such as errors.New / fmt.Errorf.
func errorType(err error) string {
	t := reflect.TypeOf(err)
	if t == nil {
		return "error"
	}
	// errors.New and fmt.Errorf produce unexported types like *errors.errorString
	// or *fmt.wrapError whose names carry no signal; normalize them to "error".
	name := t.String()
	switch name {
	case "*errors.errorString", "*fmt.wrapError", "*errors.joinError":
		return "error"
	}
	return name
}

// ---- Package-level convenience wrappers over the default client ----

// CaptureException reports err using the default client configured by Init.
func CaptureException(err error, opts ...Option) string {
	return Default().CaptureException(err, append([]Option{WithSkip(1)}, opts...)...)
}

// CaptureExceptionContext reports err using the default client, bounded by ctx.
func CaptureExceptionContext(ctx context.Context, err error, opts ...Option) string {
	return Default().CaptureExceptionContext(ctx, err, append([]Option{WithSkip(1)}, opts...)...)
}

// CaptureEvent records an event using the default client.
func CaptureEvent(eventType string, props map[string]any) {
	Default().CaptureEvent(eventType, props)
}

// CaptureEventContext records an event using the default client, bounded by ctx.
func CaptureEventContext(ctx context.Context, eventType string, props map[string]any) {
	Default().CaptureEventContext(ctx, eventType, props)
}
