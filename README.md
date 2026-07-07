# rewindrewind-go

The official Go SDK for [RewindRewind](https://rewindrewind.com) — capture
exceptions and product events from your Go services.

- **Standard library only.** No third-party dependencies (`net/http`,
  `encoding/json`, `runtime`).
- **Context-aware.** Every capture has a `…Context` variant.
- **Accurate stack traces.** Captured at the call site via `runtime.Callers`,
  with correct `in_app` classification so RewindRewind picks the right culprit.
- **Safe by construction.** Capture never panics the caller and never blocks for
  more than the configured timeout (default 2s).

```
go get rewindrewind.com/go
```

## Quick start

```go
package main

import (
	"errors"
	"os"

	"rewindrewind.com/go"
)

func main() {
	rewindrewind.Init(rewindrewind.Config{
		Key:         os.Getenv("REWINDREWIND_PROJECT_KEY"), // rrpub_…
		Environment: "production",
		Release:     "v1.2.3",
	})

	if err := charge(); err != nil {
		rewindrewind.CaptureException(err)
	}
}
```

The endpoint defaults to `https://rewindrewind.com`. Override it with
`Config.Endpoint` or the `REWINDREWIND_ENDPOINT` environment variable.

## Configuration

```go
client := rewindrewind.New(rewindrewind.Config{
	Key:         "rrpub_…",            // required: project public ingestion key
	Environment: "production",          // required: ≤64 chars
	Release:     "v1.2.3",              // optional
	Tags:        map[string]string{"service": "checkout"}, // merged into every payload
	Timeout:     2 * time.Second,       // optional, default 2s
	Enabled:     rewindrewind.Bool(true),
	OnError:     func(err error) { log.Println("rewindrewind:", err) }, // optional debug hook
})
```

`New` returns a `*Client` you can pass around; `Init` additionally installs a
package-level default used by the top-level `CaptureException` / `CaptureEvent` /
`Middleware` / `Recover` helpers.

## Capturing exceptions

```go
client.CaptureException(err)

// Per-call options:
client.CaptureException(err,
	rewindrewind.WithLevel("fatal"),
	rewindrewind.WithIdentity(rewindrewind.Identity{ID: "u_42", Email: "a@b.com"}),
	rewindrewind.WithTag("region", "us-east"),
	rewindrewind.WithExtra("order_id", 12345),
	rewindrewind.WithFingerprint("checkout-charge-failed"),
)

// Context-aware (request-scoped deadlines/cancellation):
client.CaptureExceptionContext(ctx, err)
```

`CaptureException` returns the server-assigned event ID, or `""` if disabled or
the send failed.

## Capturing events

```go
client.CaptureEvent("checkout_completed", map[string]any{
	"amount":   4999,
	"currency": "usd",
})
```

## HTTP middleware

```go
mux := http.NewServeMux()
mux.HandleFunc("/", handler)

// Recovers panics in handlers, reports them with request context, responds 500.
http.ListenAndServe(":8080", rewindrewind.Middleware(mux))
```

## Recovering goroutines

```go
go func() {
	defer client.Recover()        // report, then re-panic (process still crashes)
	// or: defer client.RecoverSilent()  // report and swallow
	doBackgroundWork()
}()
```

## How `in_app` / culprit detection works

RewindRewind derives an issue's **culprit** from the first stack frame marked
`in_app: true` (falling back to the last frame). This SDK marks a frame `in_app`
when it is **first-party application code** — i.e. NOT under `GOROOT` (standard
library / runtime) and NOT under a module cache path (`/go/pkg/mod/`,
`/pkg/mod/`). The SDK's own frames are always elided. The result is that the
culprit points at the line in your code where the error was captured, not at the
SDK or the standard library.

## License

MIT.
