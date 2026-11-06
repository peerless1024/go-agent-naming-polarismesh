English | [Chinese](test.zh_CN.md)

# Quickstart Test

The verify script runs the same Provider and Client programs shown in
[README.md](README.md).

### [case 1.1 unary discovery and call]

Operation: register the deterministic Agent through Polaris, discover it with
the Runtime Resolver, and send one non-streaming request.

Expected: unary result succeeds with non-empty `quickstart|...|unary:hello`
output.

### [case 1.2 streaming discovery and call]

Operation: send a request with streaming enabled.

Expected: more than one Agent event is received and the completed response
contains `quickstart|...|stream`.

## Cleanup

The verify script does not stop processes. `cleanup.sh --force` stops the
tracked Provider and removes local artifacts. Add `--delete-service` to remove
the remote service.
