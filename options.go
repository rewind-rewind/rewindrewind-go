package rewindrewind

// User identifies the affected end user for an exception.
type User struct {
	ID    string `json:"id,omitempty"`
	Email string `json:"email,omitempty"`
}

// captureOptions accumulates per-call overrides applied by Option values.
type captureOptions struct {
	level       string
	message     string
	fingerprint string
	user        *User
	tags        map[string]string
	extra       map[string]any
	request     map[string]any
	skip        int
}

// Option customizes a single CaptureException call.
type Option func(*captureOptions)

// WithLevel overrides the severity level (default "error").
func WithLevel(level string) Option {
	return func(o *captureOptions) { o.level = level }
}

// WithMessage overrides the human-readable message. By default the error's
// Error() string is used.
func WithMessage(message string) Option {
	return func(o *captureOptions) { o.message = message }
}

// WithFingerprint sets an explicit grouping fingerprint for the issue.
func WithFingerprint(fingerprint string) Option {
	return func(o *captureOptions) { o.fingerprint = fingerprint }
}

// WithUser attaches the affected user.
func WithUser(user User) Option {
	return func(o *captureOptions) { u := user; o.user = &u }
}

// WithTag adds a single tag, merged over the client's default tags.
func WithTag(key, value string) Option {
	return func(o *captureOptions) {
		if o.tags == nil {
			o.tags = map[string]string{}
		}
		o.tags[key] = value
	}
}

// WithTags merges multiple tags over the client's default tags.
func WithTags(tags map[string]string) Option {
	return func(o *captureOptions) {
		if o.tags == nil {
			o.tags = make(map[string]string, len(tags))
		}
		for k, v := range tags {
			o.tags[k] = v
		}
	}
}

// WithExtra attaches an arbitrary extra context value.
func WithExtra(key string, value any) Option {
	return func(o *captureOptions) {
		if o.extra == nil {
			o.extra = map[string]any{}
		}
		o.extra[key] = value
	}
}

// WithRequest attaches HTTP request context (method, url, headers, …).
func WithRequest(request map[string]any) Option {
	return func(o *captureOptions) { o.request = request }
}

// WithSkip elides additional leading stack frames. Use it when wrapping
// CaptureException inside your own helper so the culprit lands on the real call
// site rather than the wrapper.
func WithSkip(frames int) Option {
	return func(o *captureOptions) { o.skip += frames }
}

func newCaptureOptions(opts []Option) captureOptions {
	o := captureOptions{level: "error"}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}
