# ma-go

Go SDK for miniargus, a self-hosted observability platform. Two
independent packages your application imports to talk to the local
miniargus agent — neither has any dependency on the other, or on anything
outside the Go standard library.

```sh
go get github.com/Digital-Garden-LLC/ma-go
```

## tracing — HTTP request tracing

Wraps an `http.Handler` and fires one span per request to the agent's local
UDP listener (`127.0.0.1:8126` by default) — fire-and-forget, so an
unreachable or slow agent never adds latency to the wrapped handler.

```go
import "github.com/Digital-Garden-LLC/ma-go/tracing"

mux := http.NewServeMux()
mux.HandleFunc("/checkout", checkoutHandler)
// ... the rest of your routes

handler := tracing.Middleware(mux, tracing.WithServiceName("checkout-api"))
http.ListenAndServe(":8080", handler)
```

Wrap your top-level handler **once**, wherever you already assemble your
router — not inside individual route handlers. `Middleware` accepts any
`http.Handler`, so it works with `net/http`'s `ServeMux` or anything else
that satisfies that interface (e.g. `chi.Router`).

- `tracing.WithServiceName(name)` — sets the `service` every span from this
  process is tagged with. Every application should set its own name; it's
  what distinguishes one app from another in miniargus's `traces` table and
  in Grafana's `$service` dashboard variable.
- `tracing.WithAgentAddr(addr)` — overrides where spans are sent, if the
  agent isn't listening on the default `127.0.0.1:8126`.

Incoming `traceparent` headers (W3C Trace Context) are honored — a request
arriving with one continues that trace as a child span; otherwise a new
root span is started.

### Child spans

`Middleware` installs the root span on the request's `context.Context`, so
anything the handler calls can nest a child span under it with `StartSpan`
— a DB query, a downstream HTTP call, a cache lookup:

```go
func checkoutHandler(w http.ResponseWriter, r *http.Request) {
    ctx, span := tracing.StartSpan(r.Context(), "db.query")
    rows, err := db.QueryContext(ctx, "SELECT ...")
    span.SetError(err) // no-op if err is nil
    span.Finish()
    // ...
}
```

- `StartSpan(ctx, name)` returns a new `context.Context` carrying the child
  span as current — pass it into whatever the operation itself calls, so
  further nesting works — and the `*Span` itself. Safe to call even outside
  a traced request (e.g. in a background job): it just starts a new root of
  its own rather than panicking.
- `span.SetTag(key, value)` attaches an arbitrary tag.
- `span.SetError(err)` marks the span failed and records `err`'s message;
  a no-op if `err` is nil, so it's always safe to call unconditionally.
- `span.Finish()` computes duration and sends the span — typically
  deferred immediately after `StartSpan`. Idempotent, so a deferred
  `Finish()` alongside an explicit one on an error path won't double-send.

A child span's `name` (e.g. `"db.query"`) is sent in its own field, distinct
from the root HTTP span's `method`/`path`/`status` — the root span leaves
`name` empty (it's already fully identified by method/path), and every
other span leaves `method`/`path`/`status` at their zero values.

## events — custom application events

Delivers arbitrary application events to the local agent over a Unix
socket, asynchronously.

```go
import "github.com/Digital-Garden-LLC/ma-go/events"

client := events.NewClient("/tmp/miniargus-agent.sock") // match the agent's --socket flag
defer client.Close()

client.Emit(events.Event{
    Name: "order.completed",
    Tags: map[string]string{"plan": "pro"},
    Payload: json.RawMessage(`{"order_id":"1234","amount_cents":4999}`),
})
```

`Emit` never blocks the caller — events queue in a bounded channel and are
dropped (not buffered indefinitely) if the agent is unreachable or too slow
to keep up, the same drop-under-backpressure philosophy the agent itself
uses for its own buffers.

## Non-Go applications

There's no equivalent SDK for other languages yet. If you're already
instrumented with [OpenTelemetry](https://opentelemetry.io/), you can skip
this SDK and the local agent entirely — point your OTLP/HTTP exporter
(**JSON encoding, not protobuf** — the ingestion API only decodes
OTLP/HTTP JSON) directly at your miniargus deployment's
`POST /v1/ingest/traces` with your tenant's `X-API-Key`. See your
miniargus deployment's setup docs for the exact endpoint and any
auth/header details specific to your tenant.

## Versioning

Pre-1.0: the API may still change. Pin a specific commit or tag if you need
stability, and check the changelog (commit history for now) before
upgrading.

## License

Apache 2.0 — see [LICENSE](LICENSE).
