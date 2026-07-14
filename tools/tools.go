//go:build tools

/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tools

import (
	_ "github.com/bufbuild/buf/cmd/buf"
	_ "github.com/client9/misspell/cmd/misspell"
	_ "github.com/fzipp/gocyclo/cmd/gocyclo"
	_ "github.com/google/addlicense"
	_ "github.com/gordonklaus/ineffassign"
	_ "github.com/maxbrunsfeld/counterfeiter/v6"
	_ "github.com/onsi/ginkgo/v2/ginkgo"
	_ "golang.org/x/perf/cmd/benchstat"
	_ "golang.org/x/tools/cmd/goimports"
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
	_ "honnef.co/go/tools/cmd/staticcheck"
)
