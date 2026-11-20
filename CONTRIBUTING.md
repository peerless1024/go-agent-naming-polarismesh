# Contributing

Contributions are welcome through GitHub issues and pull requests.

## Before making a change

- Open an issue to discuss significant changes or new public APIs.
- Keep changes focused and preserve existing public behavior unless a change has
  been explicitly agreed upon.
- Follow the tRPC contribution process, including the
  [Tencent Contributor License Agreement](https://github.com/trpc-group/cla-database/blob/main/Tencent-Contributor-License-Agreement.md)
  for code contributions.

## Pull requests

- Write code, source comments, commit messages, pull-request text, and the
  primary Markdown documentation in English. Keep Chinese `*.zh_CN.md`
  counterparts in sync when a guide is maintained in both languages.
- Describe the problem, resulting behavior, and validation performed.
- Format Go code with `gofmt` and include tests for behavior changes.
- Run `go mod tidy`, `go build ./...`, `go vet ./...`, and `go test ./...`
  before submitting code changes. Use `go test -race -count=1 ./...` for
  concurrency, cancellation, or lifecycle changes. Document any check that
  could not be run.
- When changing A2A, MCP, Skill, AgentCard registration, discovery, or cleanup,
  also run the relevant real-service suite from `integration/`. Use
  `integration/verify_all.sh` with a reachable `POLARIS_SERVER`, and document
  the run ID, result, and cleanup outcome.
- Keep `examples/` focused on user-facing runnable flows. Put comprehensive
  failure-path and lifecycle verification in `integration/`; keep English and
  `*.zh_CN.md` guidance synchronized.
- Use the prefix of the affected package in the title when applicable, for example
  `agentcard: validate endpoint metadata`.

## Copyright and license

Include a Tencent copyright notice and an Apache 2.0 license notice in new Go
files, with the year the file is first added. Preserve existing copyright and
attribution notices.

The project is licensed under the [Apache License, Version 2.0](LICENSE).
