/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package harness

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSkillOwnership(t *testing.T) {
	cfg := &Config{Namespace: "go-agent-naming-e2e", RunID: "retained-run"}
	owned := &consoleResponse{Resource: &skillResource{
		Name:        "skill",
		Namespace:   cfg.Namespace,
		VersionInfo: &skillVersionInfo{ActiveVersion: "1.0.0"},
		Ext: map[string]string{
			ownerKey: owner,
			runIDKey: cfg.RunID,
		},
	}}
	require.NoError(t, cfg.validateSkillOwnership(owned, "skill"))
	require.True(t, skillVersionActive(owned, "1.0.0"))
	require.False(t, skillVersionActive(owned, "2.0.0"))

	owned.Resource.Ext[runIDKey] = "other-run"
	require.ErrorContains(t, cfg.validateSkillOwnership(owned, "skill"), "not owned")

	owned.Resource.Ext[runIDKey] = cfg.RunID
	owned.Resource.Name = "other-skill"
	require.ErrorContains(t, cfg.validateSkillOwnership(owned, "skill"), "identity")

	require.ErrorContains(t, cfg.validateSkillOwnership(&consoleResponse{}, "skill"), "missing")
}
