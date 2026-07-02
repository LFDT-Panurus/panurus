.PHONY: checks
checks: licensecheck gofmt goimports govet gofix misspell ineffassign staticcheck protos-lint buf-format tidy-check

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
