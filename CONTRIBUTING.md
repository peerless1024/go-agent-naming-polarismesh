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

- Write the title, description, source comments, and Go documentation in English.
- Describe the problem, resulting behavior, and validation performed.
- Format Go code with `gofmt` and include tests for behavior changes.
- Once Go modules are added, run the relevant module's `go test ./...` before
  submitting code changes. Document any checks that could not be run.
- Use the prefix of the affected package in the title when applicable, for example
  `agentcard: validate endpoint metadata`.

## Copyright and license

Include a Tencent copyright notice and an Apache 2.0 license notice in new Go
files, with the year the file is first added. Preserve existing copyright and
attribution notices.

The project is licensed under the [Apache License, Version 2.0](LICENSE).
