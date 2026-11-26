# Unit tests of every Go module. CI runs each target in UNIT_TEST_TARGETS as its own parallel job:
# .github/workflows/tests.yml without the race detector, and
# .github/workflows/nightly-race-unit-tests.yml with RACE=1. See docs/development/testing.md.

# RACE=1 runs the unit tests with the race detector.
RACE ?=
ifeq ($(RACE),1)
RACE_FLAG = -race
export GORACE = history_size=7
endif

# COVERAGE=1 makes unit-tests-main and unit-tests-evm write a
# coverage profile of their module (-coverpkg=./...) to COVERAGE_PROFILE. Each target overwrites
# COVERAGE_PROFILE, so use it with one target at a time.
COVERAGE ?=
COVERAGE_PROFILE ?= $(CURDIR)/profile.cov
ifeq ($(COVERAGE),1)
UNIT_COVER_FLAGS = -coverpkg=./... -coverprofile=$(COVERAGE_PROFILE)
else
UNIT_COVER_FLAGS = -cover
endif

# UNIT_GO_TEST runs the tests. ci/scripts/unit-tests-mk-test.sh sets it to echo to read the packages
# that each target selects.
UNIT_GO_TEST = go test

# UNIT_TEST_TARGETS lists one job per module, or per group of cmd modules. unit-tests-check
# verifies that the targets test every module and that the CI matrices run exactly these targets.
UNIT_TEST_TARGETS := unit-tests-main unit-tests-hashicorp unit-tests-evm unit-tests-integration unit-tests-cmd

# Every cmd/* module is tested by unit-tests-cmd. UNIT_TEST_EXEMPT_MODULES lists the modules that
# must have no tests.
UNIT_TEST_CMD_MODULES := $(patsubst %/go.mod,%,$(wildcard cmd/*/go.mod))
UNIT_TEST_EXEMPT_MODULES := tools
UNIT_TEST_WORKFLOWS := .github/workflows/tests.yml .github/workflows/nightly-race-unit-tests.yml

.PHONY: unit-tests
# run the unit tests of every module (every target in UNIT_TEST_TARGETS, one after the other)
unit-tests: $(UNIT_TEST_TARGETS)

.PHONY: unit-tests-race
# run the unit tests of every module with the race detector
unit-tests-race:
	@$(MAKE) unit-tests RACE=1

# The regression packages of the main module (import path contains UNIT_REGRESSION_RE) run in their
# own go test call, without the default test timeout.
UNIT_REGRESSION_RE = regression

.PHONY: unit-tests-main
# run the unit tests of the main module (most selector and storage tests need `make testing-docker-images`)
unit-tests-main:
	@all="$$(go list ./...)" || exit 1; \
	pkgs="$$(printf '%s\n' "$$all" | grep -v '$(UNIT_REGRESSION_RE)')"; \
	regression="$$(printf '%s\n' "$$all" | grep '$(UNIT_REGRESSION_RE)')"; \
	if [ -z "$$pkgs" ] || [ -z "$$regression" ]; then echo "$@: no packages or no regression packages found" >&2; exit 1; fi; \
	rc=0; \
	$(UNIT_GO_TEST) $(RACE_FLAG) $(UNIT_COVER_FLAGS) $$pkgs || rc=1; \
	$(UNIT_GO_TEST) $(RACE_FLAG) -timeout 0 -cover $$regression || rc=1; \
	exit $$rc

.PHONY: unit-tests-hashicorp
# run the hashicorp Vault module unit tests (need `make testing-docker-images`)
unit-tests-hashicorp:
	cd token/services/storage/db/kvs/hashicorp && $(UNIT_GO_TEST) $(RACE_FLAG) -cover ./...

.PHONY: unit-tests-evm
# run the evm network driver module unit tests (the anvil e2e suite needs Foundry)
unit-tests-evm:
	cd x/token/services/network/evm && $(UNIT_GO_TEST) $(RACE_FLAG) $(UNIT_COVER_FLAGS) ./...

# The integration module holds the Ginkgo integration suites and plain unit tests. A package is a
# unit-test package when it has tests and none of its test files import Ginkgo.
UNIT_INTEGRATION_PKGS_CMD = go list \
	-f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}} {{join .TestImports " "}} {{join .XTestImports " "}}{{end}}' ./... | \
	awk '$$1 != "" && !/ github\.com\/onsi\/ginkgo/ {print $$1}'

.PHONY: unit-tests-integration
# run the integration-module unit tests (the packages without Ginkgo suites)
unit-tests-integration:
	@cd integration && pkgs="$$($(UNIT_INTEGRATION_PKGS_CMD))"; \
	if [ -z "$$pkgs" ]; then echo "$@: no unit-test packages found" >&2; exit 1; fi; \
	$(UNIT_GO_TEST) $(RACE_FLAG) -cover $$pkgs

.PHONY: unit-tests-cmd
# run the unit tests of every cmd/* module, reporting every failing module
unit-tests-cmd:
	@fail=""; \
	for m in $(UNIT_TEST_CMD_MODULES); do \
		echo "Testing module $$m"; \
		(cd $$m && $(UNIT_GO_TEST) $(RACE_FLAG) -cover ./...) || fail="$$fail $$m"; \
	done; \
	if [ -n "$$fail" ]; then echo "$@: failing modules:$$fail" >&2; exit 1; fi

.PHONY: unit-tests-check
# check that UNIT_TEST_TARGETS test every tracked Go module, that the exempt modules have no tests,
# and that every workflow in UNIT_TEST_WORKFLOWS runs exactly UNIT_TEST_TARGETS with the same
# docker and foundry flags. The recipe calls $(MAKE), so it also runs under `make -n`; it only reads.
unit-tests-check: SHELL := /bin/bash
unit-tests-check:
	@fail=0; \
	recipes="$$($(MAKE) -s -n --no-print-directory $(UNIT_TEST_TARGETS) 2>&1)"; \
	for m in $$(git ls-files -- go.mod '*/go.mod' | xargs -n1 dirname | sort -u); do \
		if [[ " $(UNIT_TEST_EXEMPT_MODULES) " == *" $$m "* ]]; then \
			if [ -n "$$(git ls-files -- "$$m/*_test.go")" ]; then \
				echo "$@: exempt module $$m has tests; add a target for it" >&2; fail=1; \
			fi; \
		elif [ "$$m" = . ]; then \
			[[ " $(UNIT_TEST_TARGETS) " == *" unit-tests-main "* ]] || { echo "$@: no target tests the root module" >&2; fail=1; }; \
		elif ! grep -qE "(cd $$m && |for m in (.* )?$$m( |;))" <<<"$$recipes"; then \
			echo "$@: module $$m has no target in UNIT_TEST_TARGETS" >&2; fail=1; \
		fi; \
	done; \
	targets="$$(printf '%s\n' $(UNIT_TEST_TARGETS) | sort)"; \
	first=""; \
	for w in $(UNIT_TEST_WORKFLOWS); do \
		if [ ! -f "$$w" ]; then echo "$@: $$w not found" >&2; fail=1; continue; fi; \
		jobs="$$(sed -nE 's/^[[:space:]]*- tests:[[:space:]]*([^[:space:]#]+).*/\1/p' "$$w" | sort)"; \
		if [ "$$jobs" != "$$targets" ]; then \
			echo "$@: the '- tests:' entries of $$w do not match UNIT_TEST_TARGETS:" >&2; \
			diff <(echo "$$targets") <(echo "$$jobs") >&2; fail=1; \
		fi; \
		flags="$$(sed -nE 's/^[[:space:]]*- tests:[[:space:]]*([^[:space:]#]+).*/\1/p; s/^[[:space:]]+(docker|foundry):[[:space:]]*true.*/  \1/p' "$$w")"; \
		if [ -z "$$first" ]; then first="$$w"; first_flags="$$flags"; \
		elif [ "$$flags" != "$$first_flags" ]; then \
			echo "$@: the docker/foundry flags of $$w differ from $$first:" >&2; \
			diff <(echo "$$first_flags") <(echo "$$flags") >&2; fail=1; \
		fi; \
	done; \
	exit $$fail

.PHONY: unit-tests-mk-test
# test the targets of this file (race flag, package selection, module and workflow checks)
unit-tests-mk-test:
	./ci/scripts/unit-tests-mk-test.sh
