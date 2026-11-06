English | [Chinese](test.zh_CN.md)

# Cancellation Demo Test

### [case 3.1 Polaris discovery and streaming]

Operation: discover a Provider through Polaris and start a streaming request.

Expected: at least one non-empty partial event is received.

### [case 3.2 cancel discovered remote Agent stream]

Operation: cancel the run context after the first event and continue draining.

Expected: the client event channel closes within 3 seconds.

### [case 3.3 Provider releases canceled run]

Operation: query `/e2e/stats` on the discovered Provider endpoint.

Expected: `active=0` and `canceled>0`.

## Cleanup

The verify script does not stop processes. Default `cleanup.sh --force` stops
the Provider and removes local artifacts. Add `--delete-service` to remove the
remote service.
