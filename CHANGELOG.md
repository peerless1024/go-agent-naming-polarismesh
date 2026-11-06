# Changelog

All notable changes to this project will be documented in this file.

## v0.1.0 - Unreleased

- Add Runtime configuration, optional SDK context ownership, and lifecycle
  management.
- Build and validate A2A v1 AgentCards and the frozen instance metadata
  contract.
- Integrate dynamic metadata providers with `go-naming-polarismesh`.
- Integrate the Polaris `AgentAPI` and `SkillAPI` contracts through the
  borrowed SDK context.
- Add Skill reference merging, installation validation, and
  `skill.Repository` assembly.
- Add healthy-instance discovery, AgentCard endpoint validation, sticky
  selection, bad-card skipping, metrics, and cached-card fallback.
- Add A2A client and server helpers.
- Add runnable quickstart, real Skill, and cancellation demos with bilingual
  guides and guarded cleanup.
- Add a 24-case real Polaris E2E suite with state-scoped cleanup, Provider
  identity checks, and explicit service/Skill deletion.
- Require Polaris `v2.2.0.0` or newer for clearing service Skill references
  with explicit empty `extended_metadata`.
- Add tests, race validation, README documentation, and CI.
