English | [Chinese](test.zh_CN.md)

# Skill Demo Test

### [case 2.1 publish real Skill]

Operation: publish `trpc-agent-naming-demo-skill@1.0.0` through the real Polaris
Skill Console API, then wait for the same version through `SkillAPI.GetSkill`
and a real ZIP `SkillAPI.DownloadSkill`.

Expected: Console reports `published`, and the Skill data plane returns matching
identity, ZIP content type, content, and a SHA-256 digest.

### [case 2.2 install and execute real Skill]

Operation: start the Provider with the explicit SkillRef, wait for registration,
discover it through Polaris, and call the Agent.

Expected: the response contains the installed Skill name, version, and the
`POLARIS_DEMO_SKILL=1.0.0` marker from `SKILL.md`.

## Cleanup

The verify script does not stop processes. Default `cleanup.sh --force` stops
the Provider and removes local artifacts. Add `--delete-service` to remove the
demo Skill and service.
