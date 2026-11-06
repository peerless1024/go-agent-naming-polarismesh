English | [Chinese](README.zh_CN.md)

# Quickstart

This example connects a deterministic tRPC-Agent-Go Agent to Polaris:

```text
Provider -> Runtime.Prepare -> Polaris Registry -> A2A v1
Client   -> Runtime.Resolver -> Polaris Discover -> unary + streaming
```

No LLM key is required.

## Run

Set the Polaris host once:

```bash
export POLARIS_SERVER=your-polaris-host
```

Terminal 1:

```bash
go run ./examples/quickstart/provider
```

Terminal 2:

```bash
go run ./examples/quickstart/client
```

The Client prints one unary and one streaming result:

```json
[
  {
    "mode": "unary",
    "success": true,
    "events": 2,
    "response": "quickstart|v1.0.0|unary:hello"
  },
  {
    "mode": "stream",
    "success": true,
    "events": 5,
    "response": "quickstart|v1.0.0|stream"
  }
]
```

Defaults:

| Item | Value |
| --- | --- |
| Namespace | `go-naming-polarismesh-quickstart` |
| Service | `trpc.polaris.demo.quickstart` |
| A2A path | `/a2a` |
| Provider port | `18191` |
| Polaris gRPC | `8091` |

Use `-polaris-port`, `-namespace`, `-service`, or `-port` to override them.
Press `Ctrl+C` on the Provider to exercise graceful deregistration.

## Verify

```bash
./examples/quickstart/verify.sh --polaris-server "$POLARIS_SERVER"
./examples/quickstart/cleanup.sh --dry-run
./examples/quickstart/cleanup.sh --force
./examples/quickstart/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

`verify.sh` leaves the Provider running. `cleanup.sh` alone stops the tracked
process and removes local artifacts; `--delete-service` additionally removes
the remote service. See [test.md](test.md) for the cases.
