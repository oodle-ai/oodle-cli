//go:build tools
// +build tools

// Package client pins build-time tool dependencies so they remain in go.mod
// even after `go mod tidy`. The blank imports here are only resolved when
// building with `-tags tools`, but they cause `go mod tidy` to track the
// modules under `require` instead of removing them.
package client

import (
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
)
