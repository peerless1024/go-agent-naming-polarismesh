English | [Chinese](README.zh_CN.md)

# Cancellation Demo

This example shows the correct cancellation sequence for an A2A stream resolved
through Polaris:

```text
Runner.Run(ctx, ...)
    -> receive an Agent event
    -> cancel ctx
    -> keep draining until the event channel closes
```

No LLM key is required.

## Run

```bash
export POLARIS_SERVER=your-polaris-host
```

Terminal 1:

```bash
go run ./examples/cancel/provider
```

Terminal 2:

```bash
go run ./examples/cancel/client
```

The Client discovers the Provider, reads the first streaming event, cancels the
run context, drains the stream, and queries the Provider's `/e2e/stats` endpoint.

Successful output resembles:

```json
{
  "success": true,
  "first_event": "cancel-demo|tick-1|",
  "stream_closed_ms": 0,
  "active": 0,
  "canceled": 1
}
```

The Provider exposes:

```text
GET http://127.0.0.1:18393/e2e/stats
```

The statistics prove that the canceled run released the active slot.

## Verify

```bash
./examples/cancel/verify.sh --polaris-server "$POLARIS_SERVER"
./examples/cancel/cleanup.sh --dry-run
./examples/cancel/cleanup.sh --force
./examples/cancel/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

The verify script leaves the Provider running so it can be inspected or cleaned
separately. Default cleanup stops the tracked process and removes local
artifacts; `--delete-service` also removes the remote service.
