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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// EchoAgent is a deterministic streaming Agent used to validate wire behavior.
type EchoAgent struct {
	Name     string
	Version  string
	Active   atomic.Int64
	Canceled atomic.Int64
}

// Info implements agent.Agent.
func (a *EchoAgent) Info() agent.Info {
	return agent.Info{Name: a.Name, Description: "Deterministic integration echo agent"}
}

// Tools implements agent.Agent.
func (*EchoAgent) Tools() []tool.Tool { return nil }

// SubAgents implements agent.Agent.
func (*EchoAgent) SubAgents() []agent.Agent { return nil }

// FindSubAgent implements agent.Agent.
func (*EchoAgent) FindSubAgent(string) agent.Agent { return nil }

// Run implements agent.Agent.
func (a *EchoAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is required")
	}
	a.Active.Add(1)
	out := make(chan *event.Event, 16)
	go func() {
		defer close(out)
		defer a.Active.Add(-1)
		emit := func(response *model.Response) bool {
			select {
			case out <- event.NewResponseEvent(invocation.InvocationID, a.Name, response):
				return true
			case <-ctx.Done():
				return false
			}
		}
		message := invocation.Message.Content
		switch message {
		case "hold":
			for index := 0; ; index++ {
				partial := &model.Response{
					ID:        invocation.InvocationID,
					IsPartial: true,
					Choices: []model.Choice{{
						Delta: model.NewAssistantMessage(fmt.Sprintf("hold-%d|", index)),
					}},
				}
				if !emit(partial) {
					a.Canceled.Add(1)
					return
				}
				select {
				case <-ctx.Done():
					a.Canceled.Add(1)
					return
				case <-time.After(25 * time.Millisecond):
				}
			}
		case "stream":
			for index := 0; index < 3; index++ {
				partial := &model.Response{
					ID:        invocation.InvocationID,
					IsPartial: true,
					Choices: []model.Choice{{
						Delta: model.NewAssistantMessage(fmt.Sprintf("stream-%d|", index)),
					}},
				}
				if !emit(partial) {
					return
				}
			}
		}
		final := &model.Response{
			ID:   invocation.InvocationID,
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("echo|" + a.Version + "|" + message),
			}},
		}
		if !emit(final) {
			return
		}
		emit(&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		})
	}()
	return out, nil
}

// ScriptedSkillModel loads a Skill and records whether its body reached the
// next model request.
type ScriptedSkillModel struct {
	SkillName string
	Marker    string
	Calls     atomic.Int64
	SawMarker atomic.Bool

	mu   sync.Mutex
	step int
}

// Info implements model.Model.
func (*ScriptedSkillModel) Info() model.Info {
	return model.Info{Name: "scripted-skill-model"}
}

// GenerateContent implements model.Model.
func (m *ScriptedSkillModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request != nil {
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "POLARIS_SKILL_MARKER="+m.Marker) {
				m.SawMarker.Store(true)
			}
		}
	}
	m.Calls.Add(1)
	m.mu.Lock()
	m.step++
	step := m.step
	m.mu.Unlock()

	var response *model.Response
	switch step {
	case 1:
		response = toolCallResponse(
			"skill-load-1",
			"skill_load",
			[]byte(fmt.Sprintf(`{"skill":%q}`, m.SkillName)),
		)
	default:
		response = assistantResponse("skill-loaded")
	}
	out := make(chan *model.Response, 1)
	go func() {
		defer close(out)
		select {
		case <-ctx.Done():
		case out <- response:
		}
	}()
	return out, nil
}

func toolCallResponse(id, name string, arguments []byte) *model.Response {
	return &model.Response{
		ID:        id,
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   id,
					Function: model.FunctionDefinitionParam{
						Name:      name,
						Arguments: arguments,
					},
				}},
			},
		}},
	}
}

func assistantResponse(content string) *model.Response {
	return &model.Response{
		ID:        "assistant",
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	}
}
