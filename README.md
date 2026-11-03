# Polaris AI Registry integration for tRPC-Agent-Go

This repository hosts the tRPC-Agent-Go adapter for Polaris AI Registry.

## Status

The project is under development. This repository currently contains the project
documentation; an implementation and usage examples will follow.

## Scope

The adapter is intended to integrate Polaris AI Registry with tRPC-Agent-Go for:

- AI Agent metadata and AgentCard handling.
- Skill loading and assembly.
- A2A integration.

The existing [go-naming-polarismesh](https://github.com/trpc-ecosystem/go-naming-polarismesh)
plugin continues to provide service registration, discovery, load balancing, and
circuit breaking. This adapter builds on that integration.

Related projects:

- [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go): Agent framework.
- [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go): A2A protocol implementation.
- [Polaris](https://github.com/polarismesh/polaris): Service registry and governance.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Copyright (C) 2026 Tencent. All rights reserved.

Licensed under the [Apache License, Version 2.0](LICENSE).
