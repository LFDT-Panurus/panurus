# Development Tools

## Intro

This project uses a set of developer tools to ensure code quality, maintainability, and consistency.
These tools are installed from `ci/tools` using go install.

In order to install the tools, run:
```bash
  make install-tools
```

## Tools

## Code Quality & Static Analysis

### staticcheck
A comprehensive static analysis tool for Go. It catches bugs, performance issues, and style problems.
Reference: https://staticcheck.dev/docs/
```bash
go install honnef.co/go/tools/cmd/staticcheck
```

### ineffassign
Detects ineffectual assignments in Go code — variables that are assigned but never used.
Reference: https://github.com/gordonklaus/ineffassign
```bash
go install github.com/gordonklaus/ineffassign
```

### gocyclo
Calculates the cyclomatic complexity of Go functions, helping identify complex, hard-to-test code.
Reference: https://github.com/fzipp/gocyclo
```bash
go install github.com/fzipp/gocyclo/cmd/gocyclo
```

### misspell
Checks for common misspellings in code comments, documentation, and strings.
Reference: https://github.com/client9/misspell
```bash
go install github.com/client9/misspell/cmd/misspell
```

### goimports
Formats Go code like gofmt but also adds or removes import lines as needed.
Reference: https://pkg.go.dev/golang.org/x/tools/cmd/goimports
```bash
go install golang.org/x/tools/cmd/goimports
```

### addlicense
Automatically adds license headers to source files.
Reference: https://github.com/google/addlicense
```bash
go install github.com/google/addlicense
```

### govulncheck
Scans a module's dependencies for known vulnerabilities (from the Go vulnerability database) and reports only the ones actually reachable from the module's code, via call-graph analysis.
Reference: https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck
```bash
go install golang.org/x/vuln/cmd/govulncheck
```

To run it, use:
```bash
make govulncheck
```
Findings with no upstream fix that have been reviewed as not exploitable in Panurus's usage are listed, with justification, in `ci/govulncheck-allowlist.txt` — see that file before adding or removing an entry.

### golangci-lint
A fast, all-in-one Go linter that runs multiple linters in parallel. It aggregates the output of many popular static analysis tools.
Reference: https://golangci-lint.run

Installation:
```bash
# binary will be $(go env GOPATH)/bin/golangci-lint
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.11.4

golangci-lint --version
```

To run the linter, use:
```bash
make lint
```

## Testing & Mocking

### Ginkgo
A BDD-style testing framework for Go that pairs well with Gomega.
Reference: https://github.com/onsi/ginkgo
```bash
go install github.com/onsi/ginkgo/v2/ginkgo
```

### counterfeiter
Generates test doubles (fakes) from Go interfaces for unit tests.
Reference: https://github.com/maxbrunsfeld/counterfeiter
```bash
go install github.com/maxbrunsfeld/counterfeiter/v6
```

## API Documentation

### swag
Generates Swagger/OpenAPI doc for Go HTTP APIs based on code annotations.
Reference: https://github.com/swaggo/swag
```bash
go install github.com/swaggo/swag/cmd/swag
```

### protoc-gen-go
Generates Go code from .proto files with the Protocol Buffers compiler.
Reference: https://pkg.go.dev/google.golang.org/protobuf/cmd/protoc-gen-go
```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go
```