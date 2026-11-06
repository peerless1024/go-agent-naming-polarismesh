English | [Chinese](README.zh_CN.md)

# Skill Demo

This example downloads and installs a published Skill from the real Polaris
Skill Registry, then executes it through a deterministic A2A Agent.

No LLM key is required. The Skill body itself is returned over A2A as evidence
that the content came from Polaris.

## Run manually

Set the Polaris host:

```bash
export POLARIS_SERVER=your-polaris-host
```

Skill gRPC defaults to port `8094`. Override it with
`POLARIS_SKILL_GRPC_PORT` for `verify.sh`, or `-skill-address` for the direct
commands when the deployment uses another port.

Publish the demo Skill:

```bash
go run ./examples/skill/publish \
  -server "$POLARIS_SERVER" \
  -namespace go-naming-polarismesh-skill-demo \
  -skill trpc-agent-naming-demo-skill \
  -skill-version 1.0.0
```

Start the Provider:

```bash
go run ./examples/skill/provider \
  -server "$POLARIS_SERVER" \
  -namespace go-naming-polarismesh-skill-demo \
  -service trpc.polaris.demo.skill \
  -skill trpc-agent-naming-demo-skill \
  -skill-version 1.0.0
```

Run the Client:

```bash
go run ./examples/skill/client \
  -server "$POLARIS_SERVER" \
  -namespace go-naming-polarismesh-skill-demo \
  -service trpc.polaris.demo.skill \
  -skill trpc-agent-naming-demo-skill \
  -skill-version 1.0.0
```

The Provider:

- uses `SkillRefs` and `SkillAPI.Install`;
- downloads the real ZIP from Polaris;
- installs it into `-skill-cache`;
- opens the installed `skill.Repository`;
- returns the downloaded `SKILL.md` body through A2A.

## Verify

```bash
./examples/skill/verify.sh --polaris-server "$POLARIS_SERVER"
./examples/skill/cleanup.sh --dry-run
./examples/skill/cleanup.sh --force
./examples/skill/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

The verify script creates the shared E2E namespace, publishes the Skill, starts
the Provider, checks the installed content over A2A, and leaves the Provider
running. Default cleanup stops that process and removes local artifacts;
`--delete-service` also removes the demo Skill and remote service.

The publisher waits until the published Skill is readable and downloadable
through the Skill data plane, not only through Console.

See [test.md](test.md) for the cases.
