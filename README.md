# RewindRewind Go SDK

The official Go SDK for [RewindRewind](https://rewindrewind.com). It captures
exceptions and product events from Go services using only the standard library.

## Requirements

Go 1.22 or newer.

## Installation

```sh
go get rewindrewind.com/go
```

## Quick start

```go
package main

import (
	"os"

	"rewindrewind.com/go"
)

func main() {
	rewindrewind.Init(rewindrewind.Config{
		Key:         os.Getenv("REWINDREWIND_PROJECT_KEY"), // rrpub_xxx
		Environment: "production",
		Release:     "v1.2.3",
	})

	if err := charge(); err != nil {
		rewindrewind.CaptureException(err)
	}
}
```

Project keys start with `rrpub_` and are public ingestion credentials. Do not
put an admin key, which starts with `rr_`, in application code.

## Configuration

```go
client := rewindrewind.New(rewindrewind.Config{
	Key:         os.Getenv("REWINDREWIND_PROJECT_KEY"),
	Environment: "production",
	Release:     "v1.2.3",
	Tags:        map[string]string{"service": "checkout"},
	Timeout:     2 * time.Second,
	Enabled:     rewindrewind.Bool(true),
	OnError:     func(err error) { log.Println("rewindrewind:", err) },
})
```

| Field | Default | Notes |
| --- | --- | --- |
| `Key` | Empty | Required project key; an empty key disables capture |
| `Endpoint` | `REWINDREWIND_ENDPOINT`, then `https://rewindrewind.com` | HTTPS required except for loopback hosts |
| `Environment` | Empty | Required; trimmed and limited to 64 characters |
| `Release` | Empty | Optional release or Git SHA |
| `Tags` | `nil` | Merged into exception tags and event properties |
| `Timeout` | 2 seconds | Used when the SDK creates the HTTP client |
| `Enabled` | `nil`, meaning enabled | Set with `rewindrewind.Bool(false)` to disable capture |
| `HTTPClient` | SDK client | Supply a custom transport or timeout policy |
| `OnError` | `nil` | Receives nonfatal validation, transport, and encoding errors |

`New` returns a concurrency-safe `*Client`. `Init` also installs it as the
package default used by `CaptureException`, `CaptureEvent`, `Middleware`,
`Recover`, and their related helpers.

## Capturing exceptions

```go
eventID := client.CaptureException(err,
	rewindrewind.WithLevel("fatal"),
	rewindrewind.WithMessage("checkout charge failed"),
	rewindrewind.WithIdentity(rewindrewind.Identity{ID: "u_42", Email: "a@example.com"}),
	rewindrewind.WithTags(map[string]string{"region": "us-east"}),
	rewindrewind.WithExtra("order_id", 12345),
	rewindrewind.WithFingerprint("checkout-charge-failed"),
)
```

`CaptureException` returns the server-assigned event ID, or `""` when capture
is disabled or unsuccessful. `WithRequest` adds request context. `WithSkip`
removes wrapper frames when capture is called through your own helper.

Use a context to add request-scoped cancellation or deadlines:

```go
eventID := client.CaptureExceptionContext(ctx, err)
```

Capture never panics the caller. The default HTTP client limits a request to two
seconds. A custom `HTTPClient` controls its own timeout, and a context deadline
can impose an additional bound.

## Capturing events

```go
client.CaptureEvent("checkout.completed", map[string]any{
	"amount":   4999,
	"currency": "usd",
})
```

Use `CaptureEventContext` when the operation should follow a context deadline or
cancellation signal.

## HTTP middleware

```go
mux := http.NewServeMux()
mux.HandleFunc("/", handler)

wrapped := client.Middleware(mux)
log.Fatal(http.ListenAndServe(":8080", wrapped))
```

The middleware recovers handler panics, reports them with safe request context,
and responds with status 500. It does not propagate the panic.

## Recovering goroutines

```go
go func() {
	defer client.Recover()
	doBackgroundWork()
}()
```

`Recover` reports and re-panics. Use `RecoverSilent` to report and swallow the
panic instead.

## Data safety

The SDK recursively redacts common sensitive keys in tags, extra data, request
context, and event properties. Redacted values become `"[FILTERED]"`. Automatic
HTTP request context omits query strings and removes query parameters from the
referrer. The SDK also refuses non-HTTPS endpoints except for loopback hosts.

## Stack frame classification

RewindRewind derives an issue's culprit from the first stack frame marked
`in_app: true`, falling back to the last frame. The SDK captures the call site,
removes its own frames, and marks standard-library, runtime, and Go module cache
frames as non-application code. Reported paths are shortened to avoid exposing
absolute build-machine paths.

## Development

```sh
bin/test
```

## License

MIT
