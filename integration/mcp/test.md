English | [Chinese](test.zh_CN.md)

# MCP Metadata, Service Contract, Discovery and Pool

## Test Function

`TestMCPMetadataContractDiscoveryAndPool` in
[e2e_test.go](e2e_test.go).

## Processes

| Process | Count | Purpose |
| --- | --- | --- |
| `go test` test binary | 1 | Starts the MCP Streamable HTTP server, registers metadata, collects and reports the Service Contract, discovers the instance, and invokes the tool |
| MCP server | In-process | Uses the real `trpc-mcp-go` Streamable HTTP handler and a loopback listener |
| Polaris | External | Supplied by `POLARIS_SERVER`; the test does not start a Polaris server |

There is no standalone MCP server or client process. The test keeps the server
and Pool in one process so the wire path, registry write, discovery, and
cleanup can be asserted deterministically.

## Services and Resources

| Resource | Value |
| --- | --- |
| Namespace | `go-agent-naming-e2e` by default |
| Service | `gan-e2e-<run-id>-mcp` |
| Type | `MCP_SERVER` |
| Instances | 1 healthy instance |
| Instance host | `127.0.0.1` |
| Business port | Random loopback MCP port |
| Instance protocol | `mcp-streamable-http` |
| Instance version | `1.0.0` |
| Metadata | `mcp-server-name`, `mcp-protocol-version`, `mcp-endpoint-path` |
| Endpoint path | `/mcp` |
| Service Contract | Type `mcp`, with interfaces and content digest |

## Call Relationship

```text
test MCP server ──► local MCP listener
        │
        ├─► MCP initialize / ListTools ─► CollectContract
        │                                      │
        │                                      ▼
        │                              ProviderAPI.ReportServiceContract
        │
        └─► go-naming-polarismesh ─► Polaris MCP instance

test ConsumerAPI / MCP Pool
        │
        ├─► Polaris Discover ─► MCP instance ─► tools/call(echo)
        │
        └─► ConsumerAPI.GetServiceContract ─► verify contract type/digest/interfaces
```

The Pool independently discovers the MCP instance and calls the native
`echo` tool. The contract query is through `ConsumerAPI`, not through the
local MCP listener.

The test enables `provider.reportServiceContractEnable` before reporting so the
real `ProviderAPI.ReportServiceContract` path is exercised.

## Cases

### [case 1.1 metadata and registration]

Operation: ensure the MCP_SERVER service, prepare server metadata, install the
metadata provider, and register the MCP instance.

Expected: one healthy instance is visible with the MCP service name, protocol
version, endpoint path, and `mcp-streamable-http` protocol.

### [case 1.2 Contract collection and reporting]

Operation: connect to the real MCP endpoint, initialize the session, list
declared capabilities, collect the contract, and report it through
`ProviderAPI`.

Expected: the reported contract has type `mcp` and a non-empty content digest.

### [case 1.3 Pool discovery and Tool call]

Operation: create an adapter MCP Pool with a real `ConsumerAPI`, refresh it
until the instance is available, and call `echo` with value `integration`.

Expected: the tool result contains `mcp|<service>|integration`.

### [case 1.4 Contract query]

Operation: query the contract through `ConsumerAPI.GetServiceContract`.

Expected: the returned contract has type `mcp`, a non-empty digest, and at
least one interface.

## Verification

The test asserts:

- Instance metadata `mcp-server-name`, `mcp-protocol-version`, and
  `mcp-endpoint-path`.
- MCP instance protocol and health.
- Contract type, digest, and interfaces.
- Pool discovery and successful Tool call.
- Service type and ownership cleanup.

## Cleanup

The MCP registry is deregistered, the HTTP server is shut down, the metadata
provider is removed, and the MCP service and contracts are deleted in
`t.Cleanup`. The shared namespace is retained.

## Run

```bash
POLARIS_SERVER=your-polaris-host \
  go test -tags=integration -count=1 -v -timeout=10m ./integration/mcp
```
