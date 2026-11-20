English | [Chinese](README.zh_CN.md)

# Agent Skill From Polaris

This example follows the official `trpc-agent-go` Skill examples:

```text
publish -> Polaris Skill Registry
agent   -> SkillAPI.Install -> installed files -> Runtime.SkillRepository
        -> llmagent.WithSkills -> skill_load -> A2A Server -> Polaris
```

The published Skill contains `SKILL.md`, a reference document, and a script.
The Agent loads the Skill on demand through the normal `skill_load` tool.

## Prerequisites

- Go 1.21+.
- A Polaris deployment with Skill Registry support.
- An OpenAI-compatible model endpoint.

```bash
export POLARIS_SERVER=your-polaris-host
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://your-openai-compatible-endpoint/v1
```

## Run

Publish the Skill:

```bash
go run ./examples/skill/publish
```

Start the Agent:

```bash
go run ./examples/skill/agent
```

Call it through Polaris:

```bash
go run ./examples/skill/client
```

The Client asks for the Polaris greeting. The Agent should load
`polaris-greeting` and return the marker from the published `SKILL.md`.

## What Runtime.Prepare Does

The Agent uses `SkillMode=names` with one explicit reference:

```go
cfg.SkillMode = adapterconfig.SkillModeNames
cfg.SkillRefs = []adapterconfig.SkillRef{{
    Namespace: "go-agent-naming-examples",
    Name:      "polaris-greeting",
    Version:   "1.0.0",
}}
cfg.SkillCacheDir = "./.skill-cache"
```

`Prepare` calls the real Skill data plane, installs the pinned version, and
returns a `skill.Repository`. That repository is passed directly to
`llmagent.WithSkills(repo)`. No Skill body is copied into the example source
after publication.

The adapter also supports `published` mode (page through every published Skill
in the namespace) and `mounted` mode (read `extended_metadata` from the
AI_AGENT service). This example uses `names` so the installed set stays
isolated and reproducible.

## Cleanup

```bash
./examples/skill/cleanup.sh --dry-run
./examples/skill/cleanup.sh --force --delete-service --polaris-server "$POLARIS_SERVER"
```

The script deletes the Agent service and the Skill. It checks the
`examples.owner` field before deleting the Skill.

## Useful Flags

| Flag | Default |
| --- | --- |
| `-namespace` | `go-agent-naming-examples` |
| `-skill` | `polaris-greeting` |
| `-skill-version` | `1.0.0` |
| `-service` | `trpc.polaris.examples.skill-agent` |
| `-skill-cache` | system temp directory |
| `-port` | `18193` |

See the [Skill integration suite](../../integration/skill/e2e_test.go) for the
publish, install, `skill_load`, and model-injection assertion.
