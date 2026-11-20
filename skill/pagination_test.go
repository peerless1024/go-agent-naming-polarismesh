/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package skill

import (
	"context"
	"testing"

	"github.com/polarismesh/polaris-go/api"
	skillflow "github.com/polarismesh/polaris-go/pkg/flow/skill"
	"github.com/polarismesh/polaris-go/pkg/plugin/skillconnector"
	"github.com/stretchr/testify/require"
)

type rawSkillPages struct {
	skillconnector.SkillConnector
	resources []*skillconnector.Resource
	offsets   []uint32
}

func (c *rawSkillPages) GetSkillList(req *skillconnector.ListSkillsRequest) (*skillconnector.ListSkillsResponse, error) {
	c.offsets = append(c.offsets, req.Offset)
	end := req.Offset + req.Limit
	if end > uint32(len(c.resources)) {
		end = uint32(len(c.resources))
	}
	var page []*skillconnector.Resource
	if req.Offset < end {
		page = c.resources[req.Offset:end]
	}
	return &skillconnector.ListSkillsResponse{Total: uint32(len(c.resources)), Resources: page}, nil
}

type flowSkillAPI struct {
	api.SkillAPI
	flow *skillflow.Flow
}

func (a flowSkillAPI) GetSkillListResult(req *api.GetSkillListRequest) (*api.SkillListResult, error) {
	result, err := a.flow.GetSkillListResult(&req.GetSkillListRequest)
	if err != nil {
		return nil, err
	}
	return &api.SkillListResult{SkillListResult: *result}, nil
}

func TestPublishedPaginationUsesRawWindows(t *testing.T) {
	live := func(name string) *skillconnector.Resource {
		return &skillconnector.Resource{
			Namespace: "Production", Name: name, Status: "published",
			VersionInfo: &skillconnector.VersionInfo{ActiveVersion: "1.0.0"},
		}
	}
	draft := func(name string) *skillconnector.Resource {
		return &skillconnector.Resource{Namespace: "Production", Name: name, Status: "draft"}
	}
	for _, test := range []struct {
		name      string
		resources []*skillconnector.Resource
		want      []string
		offsets   []uint32
	}{
		{"filtered first page", []*skillconnector.Resource{draft("a"), draft("b"), live("c")}, []string{"c"}, []uint32{0, 2}},
		{"mixed pages", []*skillconnector.Resource{live("a"), draft("b"), draft("c"), live("d")}, []string{"a", "d"}, []uint32{0, 2}},
		{"filtered middle page", []*skillconnector.Resource{live("a"), live("b"), draft("c"), draft("d"), live("e")}, []string{"a", "b", "e"}, []uint32{0, 2, 4}},
		{"all drafts", []*skillconnector.Resource{draft("a"), draft("b"), draft("c")}, nil, []uint32{0, 2}},
		{"all published", []*skillconnector.Resource{live("a"), live("b"), live("c")}, []string{"a", "b", "c"}, []uint32{0, 2}},
		{"filtered tail", []*skillconnector.Resource{live("a"), live("b"), draft("c")}, []string{"a", "b"}, []uint32{0, 2}},
		{"empty namespace", nil, nil, []uint32{0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			connector := &rawSkillPages{resources: test.resources}
			loader := NewLoader(nil, flowSkillAPI{flow: skillflow.NewFlow(connector)})
			refs, err := loader.loadPublishedRefs(context.Background(), Options{Namespace: "Production", ListLimit: 2, MaxSkills: 10})
			require.NoError(t, err)
			var names []string
			for _, ref := range refs {
				names = append(names, ref.ref.Name)
			}
			require.Equal(t, test.want, names)
			require.Equal(t, test.offsets, connector.offsets)
		})
	}
}
