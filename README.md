# ma-go

Go SDK for [miniargus](https://github.com/Digital-Garden-LLC/miniargus), a
self-hosted observability platform. Two independent packages your
application imports to talk to the local miniargus agent — neither has any
dependency on the other, or on anything outside the Go standard library.

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
root span is started. Only one span is emitted per request today (the
top-level HTTP span); there's no child-span API yet for instrumenting
operations nested inside a handler (a DB call, a downstream request, etc.).

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
