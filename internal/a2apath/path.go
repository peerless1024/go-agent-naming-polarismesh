/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

// Package a2apath validates literal endpoints shared by configuration and cards.
package a2apath

import (
	"errors"
	"net/url"
	"strings"
)

// Validate accepts canonical literal HTTP paths, including a trailing slash.
// Errors intentionally omit input, which may contain credentials or tokens.
func Validate(value string) error {
	if !strings.HasPrefix(value, "/") {
		return errors.New("a2a path must start with /")
	}
	if strings.ContainsAny(value, "%{}\\?#\r\n\t ") {
		return errors.New("a2a path must be literal and contain no encoding, wildcard, query, or fragment")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Path != value {
		return errors.New("a2a path is not a literal HTTP path")
	}
	segments := strings.Split(strings.TrimSuffix(value, "/"), "/")
	for _, segment := range segments[1:] {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("a2a path must not contain empty, dot, or parent segments")
		}
	}
	return nil
}
