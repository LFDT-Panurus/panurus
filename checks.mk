.PHONY: checks
checks: checks-fast checks-heavy

.PHONY: checks-fast
# fast, purely textual checks (no compilation) — cheap enough to gate the
# integration-test matrix on. Run in CI's 'checks' job.
checks-fast: licensecheck gofmt goimports misspell ineffassign protos-lint buf-format tidy-check

.PHONY: checks-heavy
# compile-heavy static analysis (invokes the Go type-checker across all
# modules). Kept off the integration-test critical path — run in CI's parallel
# 'lint' job alongside golangci-lint. govulncheck also needs network access to
# fetch the vuln database, another reason to keep it off that critical path.
checks-heavy: govet gofix staticcheck govulncheck

.PHONY: checks-no-tidy
# same as 'checks' but without tidy-check; for use after a workflow step that
# already rewrote go.mod/go.sum to a new dependency version and ran 'make tidy'
# itself, where tidy-check's git-diff-against-HEAD heuristic would always fail
checks-no-tidy: licensecheck gofmt goimports govet gofix misspell ineffassign staticcheck govulncheck protos-lint buf-format

.PHONY: licensecheck
licensecheck:
	@echo Running license check
	@for dir in $(GO_MODULES); do \
		echo "  Checking licenses in module: $$dir"; \
		(cd $$dir && find . -path './.git' -prune -o -name '*.go' -not -name '*.pb.go' -print | xargs addlicense -check) || (echo "Missing license headers in $$dir"; exit 1); \
	done

.PHONY: gofmt
gofmt:
	@echo Running gofmt
	@for dir in $(GO_MODULES); do \
		echo "  Checking format in module: $$dir"; \
		(cd $$dir && { \
			OUTPUT="$$(find . -path './.git' -prune -o -name '*.go' -not -name '*.pb.go' -print | xargs gofmt -l -s || true)"; \
			if [ -n "$$OUTPUT" ]; then \
				echo "The following gofmt issues were flagged in $$dir:"; \
				echo "$$OUTPUT"; \
				echo "The gofmt command 'gofmt -l -s -w' must be run for these files"; \
				exit 1; \
			fi; \
		}) || exit 1; \
	done

.PHONY: goimports
goimports:
	@echo Running goimports
	@for dir in $(GO_MODULES); do \
		echo "  Checking imports in module: $$dir"; \
		(cd $$dir && { \
			OUTPUT="$$(find . -path './.git' -prune -o -name '*.go' -not -name '*.pb.go' -print | xargs goimports -l || true)"; \
			if [ -n "$$OUTPUT" ]; then \
				echo "The following files contain goimports errors in $$dir:"; \
				echo "$$OUTPUT"; \
				echo "The goimports command 'goimports -l -w' must be run for these files"; \
				exit 1; \
			fi; \
		}) || exit 1; \
	done

.PHONY: govet
govet:
	@echo Running go vet
	@for dir in $(GO_MODULES); do \
		echo "  Checking module: $$dir"; \
		(cd $$dir && go vet -all $$(go list ./...)) || exit 1; \
	done

.PHONY: gofix
gofix:
	@echo Running go fix
	@for dir in $(GO_MODULES); do \
		echo "  Checking module: $$dir"; \
		(cd $$dir && { \
			OUTPUT="$$(go fix -diff ./... 2>&1 | grep -v '^go: warning:' | grep -v '^no packages to fix')"; \
			if [ -n "$$OUTPUT" ]; then \
				echo "go fix found modernization opportunities in $$dir:"; \
				echo "$$OUTPUT"; \
				echo ""; \
				echo "Run 'make gofix-apply' to apply these changes automatically."; \
				exit 1; \
			fi; \
		}) || exit 1; \
	done
	@echo "✓ No go fix suggestions - code is up to date."

.PHONY: gofix-apply
gofix-apply:
	@echo Applying go fix to all modules
	@for dir in $(GO_MODULES); do \
		echo "  Applying fixes to module: $$dir"; \
		(cd $$dir && go fix ./...); \
	done
	@echo "✓ go fix applied to all modules."

.PHONY: misspell
misspell:
	@echo Running misspell
	@for dir in $(GO_MODULES); do \
		echo "  Checking spelling in module: $$dir"; \
		(cd $$dir && { \
			OUTPUT="$$(find . -path './.git' -prune -o -type f -print | grep -v '.golangci.yml' | grep -v 'testdata' | xargs misspell || true)"; \
			if [ -n "$$OUTPUT" ]; then \
				echo "The following files in $$dir have spelling errors:"; \
				echo "$$OUTPUT"; \
				exit 1; \
			fi; \
		}) || exit 1; \
	done

.PHONY: staticcheck
staticcheck:
	@echo Running staticcheck
	@for dir in $(GO_MODULES); do \
		echo "  Checking module: $$dir"; \
		(cd $$dir && { \
			OUTPUT="$$(staticcheck -tests=false ./... | grep -v .pb.go || true)"; \
			if [ -n "$$OUTPUT" ]; then \
				echo "The following staticcheck issues were flagged in $$dir:"; \
				echo "$$OUTPUT"; \
				exit 1; \
			fi; \
		}) || exit 1; \
	done

.PHONY: govulncheck
# scan for known vulnerabilities in the module's dependencies, restricted to
# vulnerabilities reachable from actually-called code (govulncheck's call-graph
# analysis, as opposed to a plain dependency-version scan). Findings listed in
# ci/govulncheck-allowlist.txt (e.g. no fix published upstream) don't fail the
# build; see that file for the reasoning behind each entry.
govulncheck:
	@echo Running govulncheck
	@for dir in $(GO_MODULES); do \
		echo "  Checking module: $$dir"; \
		(cd $$dir && $(CURDIR)/ci/scripts/govulncheck.sh) || exit 1; \
	done

.PHONY: gocyclo
gocyclo:
	@echo Running gocyclo
	@gocyclo -over 15 $(shell go list -f '{{.Dir}}' ./...) || (echo "Found some code with a Cyclomatic complexity over 15! Better refactor"; exit 1;)

.PHONY: ineffassign
ineffassign:
	@echo Running ineffassign
	@for dir in $(GO_MODULES); do \
		echo "  Checking module: $$dir"; \
		(cd $$dir && ineffassign $$(go list -f '{{.Dir}}' ./...)) || exit 1; \
	done

.PHONY: protos-lint
protos-lint:
	@echo "Linting protobuf files..."
	@buf lint

.PHONY: buf-format
buf-format:
	@echo "Checking protobuf formatting..."
	@buf format -d --exit-code

# literal '#': Make treats a bare '#' as a comment, and buf's git-input syntax
# (.git#branch=...) needs one, so build it via an escaped-hash variable.
BUF_HASH := \#
# base git ref to diff the working tree's protos against, using buf.yaml's
# `breaking:` ruleset. Defaults to the local 'main' branch for local runs; CI's
# 'proto-breaking' job overrides it with the pull request's base ref.
BUF_BREAKING_AGAINST ?= .git$(BUF_HASH)branch=main

.PHONY: protos-breaking
# detect backward-incompatible protobuf changes (wire-format breaks) against a
# base ref. Not part of 'checks-fast'/'checks': it is comparative (needs a base
# ref) and so has no meaning for a plain push — it runs PR-only in CI. Requires
# the base ref to be present in .git.
protos-breaking:
	@echo "Checking protobuf backward compatibility against '$(BUF_BREAKING_AGAINST)'..."
	@buf breaking --against '$(BUF_BREAKING_AGAINST)'

.PHONY: tidy-check
# check that go modules are tidy (run 'make tidy' to fix)
tidy-check:
	@echo "Checking Go modules are tidy..."
	@for dir in $(TIDY_GO_MODULES); do \
		echo "  Checking module: $$dir"; \
		(cd $$dir && go mod tidy) || exit 1; \
	done
	@if git diff --name-only | grep -qE '(go\.mod|go\.sum)'; then \
		echo ""; \
		echo "The following go.mod/go.sum files are not tidy:"; \
		git diff --name-only | grep -E '(go\.mod|go\.sum)'; \
		echo ""; \
		echo "Run 'make tidy' to fix this."; \
		exit 1; \
	fi
	@echo "✓ All Go modules are tidy."
