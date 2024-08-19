//go:build tools

//go:generate go build -o ./bin/mockery github.com/vektra/mockery/v2
//go:generate go get mvdan.cc/gofumpt@v0.6.0
//go:generate go get github.com/golangci/golangci-lint/cmd/golangci-lint@v1.59.1
//go:generate go build -o ./bin/swag github.com/swaggo/swag/cmd/swag

// this file references indirect dependencies that are used during the build

package main

import (
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "github.com/swaggo/swag/cmd/swag"
	_ "github.com/vektra/mockery/v2"
	_ "mvdan.cc/gofumpt"
)
