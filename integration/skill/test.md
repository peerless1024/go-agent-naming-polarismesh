English | [Chinese](test.zh_CN.md)

# Published Skill Install and Model Injection

## Test Function

`TestPublishedSkillInstallAndModelInjection` in
[e2e_test.go](e2e_test.go).

## Processes

| Process | Count | Purpose |
| --- | --- | --- |
| Prebuilt test binary | 1 | Publishes the Skill, installs it through SkillAPI, starts the in-process A2A server, discovers the Agent, and runs the scripted model |
| A2A server | In-process | Serves the real A2A handler on a loopback listener |
| Polaris | External | Provides discovery, Console, and Skill Registry APIs through `POLARIS_SERVER` |
| LLM | Not used | A scripted `model.Model` drives the real `llmagent` Skill path |

This case does not start a separate Provider, Client, or model process. The
real Skill data plane, `llmagent`, `skill_load`, and A2A wire path run inside
the test process.

## Services and Resources

| Resource | Value |
| --- | --- |
| Namespace | `go-agent-naming-e2e` by default |
| Agent service | `gan-e2e-<run-id>-skill-agent` |
| Service type | `AI_AGENT` |
| Agent instance | 1 healthy instance |
| Instance host | `127.0.0.1` |
| Registration port | `1` |
| Instance version | `v1.0.0` |
| Instance protocol | Empty |
| Skill | `gan-e2e-<run-id>-skill@1.0.0` |
| Skill files | `SKILL.md` and `references/data.md` |
| Skill marker | `marker-<run-id>` embedded in the published body |

## Call Relationship

```text
test process
   │
   ├─► Console Skill API ─► create + publish Skill
   │
   ├─► SkillAPI ─► GetSkill / DownloadSkill ─► install into cache
   │                                      │
   │                                      ▼
   │                              Skill repository
   │                                      │
   │                                      ▼
   │                              llmagent.WithSkills
   │
   └─► Consumer Runtime ─► Polaris Discover ─► A2A Agent
                                                  │
                                                  ├─► model step 1: skill_load
                                                  ├─► Skill body injected
                                                  └─► model step 2: skill-loaded
                                                         │
   ◄──────────────────── A2A response ───────────────────┘
```

The first scripted model call requests `skill_load`. The real `llmagent`
processor then loads the installed Skill body and includes the marker in the
next model request. The second call returns `skill-loaded`.

## Cases

### [case 1.1 publish and data-plane readiness]

Operation: create the Skill through the Console Skill API and publish version
`1.0.0`.

Expected: `GetSkill` and `DownloadSkill` both see the published Skill and its
non-empty ZIP content.

### [case 1.2 install and repository assembly]

Operation: prepare the provider Runtime with `SkillModeNames` and the explicit
Skill reference.

Expected: the Skill is installed from Polaris and
`runtime.SkillRepository()` returns a non-nil repository.

### [case 1.3 Skill load and prompt injection]

Operation: invoke the Agent with the scripted model.

Expected: the model first calls `skill_load`; the next model request contains
`POLARIS_SKILL_MARKER=marker-<run-id>`; the model is called at least twice.

### [case 1.4 discovery and A2A response]

Operation: resolve the Skill Agent through Polaris and invoke it through the
JSONRPC A2A interface.

Expected: the Agent returns `skill-loaded`, and the instance metadata contains
the three AgentCard keys with an empty protocol.

## Verification

The test asserts:

- Console publication and Skill data-plane readability.
- Installed repository is non-nil.
- `skill_load` is executed by the real framework.
- The published Skill marker reaches the model request.
- Model call count is at least two.
- A2A discovery and final `skill-loaded` response.
- Service and Skill cleanup ownership.

## Cleanup

The Agent Runtime is deregistered, the A2A server is shut down, the AI_AGENT
service is deleted, and the published Skill is deleted in `t.Cleanup`. The
shared namespace is retained.

Polaris versions that use second-granularity Skill cache timestamps require
`create` and `publish` to cross a second boundary. The harness deliberately
waits for the next publication window so the published status cannot be
missed by the server's `mtime > last` incremental cache query.

If the Skill endpoint load-balances multiple replicas with different cache
freshness, the harness opens a fresh SDK per verification attempt and keeps the
first connection that can both read and download the Skill. The provider
Runtime reuses that verified connection for installation.

## Run

```bash
make -C integration build
POLARIS_SERVER=your-polaris-host \
  "integration/bin/$(go env GOOS)-$(go env GOARCH)/skill.test" \
  -test.count=1 -test.v -test.timeout=10m
```
