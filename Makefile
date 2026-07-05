# pinned versions
FABRIC_VERSION ?= 3.1.4
FABRIC_CA_VERSION ?= 1.5.7
FABRIC_TWO_DIGIT_VERSION = $(shell echo $(FABRIC_VERSION) | cut -d '.' -f 1,2)

FABRIC_X_TOOLS_VERSION ?= v0.0.17
FABRIC_X_COMMITTER_VERSION ?= 1.0.0

# need to install fabric binaries outside of panuru's  tree for now (due to chaincode packaging issues)
FABRIC_BINARY_BASE=$(PWD)/../fabric
FAB_BINS ?= $(FABRIC_BINARY_BASE)/bin

# integration test options
GINKGO_TEST_OPTS ?=
GINKGO_TEST_OPTS += --keep-going -cover

TOP = .

# include the checks target
include $(TOP)/checks.mk

# Define all Go module directories
GO_MODULES := . integration token/services/storage/db/kvs/hashicorp cmd/artifactgen cmd/tokengen cmd/token_validation_service cmd/profiler cmd/skicleanup
TIDY_GO_MODULES := $(GO_MODULES) tools

# include fabricx target
include $(TOP)/fabricx.mk
# include the interop target
include $(TOP)/interop.mk
# include the fungible target
include $(TOP)/fungible.mk

all: install-tools install-softhsm checks unit-tests #integration-tests

.PHONY: install-tools
# install tools required for development and testing
install-tools:
# Thanks for great inspiration https://marcofranssen.nl/manage-go-tools-via-go-modules
	@echo Installing tools from tools/tools.go
	@cd tools; cat tools.go | grep _ | awk -F'"' '{print $$2}' | xargs -tI % go install %
	@$(MAKE) install-linter-tool

.PHONY: download-fabric
# download fabric binaries
download-fabric:
	./ci/scripts/download_fabric.sh $(FABRIC_BINARY_BASE) $(FABRIC_VERSION) $(FABRIC_CA_VERSION)

GO_TEST_PARAMS ?= -coverpkg=./... -coverprofile=profile.cov
GO_PACKAGES = $(shell go list ./... | grep -v '/integration/' | grep -v 'regression' | grep -v 'mock' |  grep -v 'protos-go' |  grep -v 'testutils'; go list ./integration/nwo/...)

.PHONY: unit-tests
# run standard unit tests
unit-tests:
	@go test $(GO_TEST_PARAMS) $(GO_PACKAGES)
	cd token/services/storage/db/kvs/hashicorp/; go test -cover ./...

.PHONY: unit-tests-race
# run unit tests with race detection
unit-tests-race:
	@export GORACE=history_size=7; go test -race -cover $(shell go list ./... | grep -v '/integration/'  | grep -v 'regression')
	cd integration/nwo/; go test -cover ./...

.PHONY: unit-tests-regression
# run regression unit tests
unit-tests-regression:
	@go test -race -timeout 0 -cover $(shell go list ./... | grep -v '/integration/' | grep 'regression')

.PHONY: install-softhsm
# install softhsm for testing
install-softhsm:
	./ci/scripts/install_softhsm.sh

.PHONY: docker-images
# build/pull docker images needed for testing
docker-images: fabric-docker-images monitoring-docker-images testing-docker-images

.PHONY: testing-docker-images
# pull docker images for testing (postgres, vault)
testing-docker-images:
	docker pull postgres:16.2-alpine
	docker tag postgres:16.2-alpine fsc.itests/postgres:latest
	docker pull hashicorp/vault

.PHONY: fabric-docker-images
# pull fabric docker images
fabric-docker-images:
	docker pull hyperledger/fabric-baseos:$(FABRIC_TWO_DIGIT_VERSION)
	docker image tag hyperledger/fabric-baseos:$(FABRIC_TWO_DIGIT_VERSION) hyperledger/fabric-baseos:latest
	docker pull hyperledger/fabric-ccenv:$(FABRIC_TWO_DIGIT_VERSION)
	docker image tag hyperledger/fabric-ccenv:$(FABRIC_TWO_DIGIT_VERSION) hyperledger/fabric-ccenv:latest

.PHONY: monitoring-docker-images
# pull monitoring docker images (explorer, prometheus, grafana, jaeger)
monitoring-docker-images:
	docker pull ghcr.io/hyperledger-labs/explorer-db:latest
	docker pull ghcr.io/hyperledger-labs/explorer:latest
	docker pull prom/prometheus:latest
	docker pull grafana/grafana:latest
	docker pull cr.jaegertracing.io/jaegertracing/jaeger:2.12.0

.PHONY: integration-tests-nft-dlog
# run nft integration tests with idemix
integration-tests-nft-dlog:
	cd ./integration/token/nft/dlog; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) .

.PHONY: integration-tests-nft-fabtoken
# run nft integration tests with fabtoken
integration-tests-nft-fabtoken:
	cd ./integration/token/nft/fabtoken; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) .

.PHONY: integration-tests-dvp-fabtoken
# run dvp integration tests with fabtoken
integration-tests-dvp-fabtoken:
	cd ./integration/token/dvp/fabtoken; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) .

.PHONY: integration-tests-dvp-dlog
# run dvp integration tests with idemix
integration-tests-dvp-dlog:
	cd ./integration/token/dvp/dlog; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) .


.PHONY: tidy
# tidy up go modules
tidy:
	@echo "Tidying Go modules..."
	@for dir in $(TIDY_GO_MODULES); do \
		echo "  Tidying module: $$dir"; \
		(cd $$dir && go mod tidy); \
	done

.PHONY: clean
# clean up docker artifacts and generated files
clean:
	docker network prune -f
	docker container prune -f
	docker volume prune -f
	rm -rf ./integration/token/fungible/dlog/out/
	rm -rf ./integration/token/fungible/dlog/testdata/
	rm -rf ./integration/token/fungible/dlogx/out/
	rm -rf ./integration/token/fungible/dloghsm/out/
	rm -rf ./integration/token/fungible/dloghsm/testdata/
	rm -rf ./integration/token/fungible/dlogstress/out/
	rm -rf ./integration/token/fungible/dlogstress/testdata/
	rm -rf ./integration/token/fungible/fabtoken/out/
	rm -rf ./integration/token/fungible/fabtoken/testdata/
	rm -rf ./integration/token/fungible/odlog/out/
	rm -rf ./integration/token/fungible/ofabtoken/out/
	rm -rf ./integration/token/fungible/mixed/out/
	rm -rf ./integration/token/nft/dlog/out/
	rm -rf ./integration/token/nft/fabtoken/out/
	rm -rf ./integration/token/nft/odlog/out/
	rm -rf ./integration/token/nft/ofabtoken/out/
	rm -rf ./integration/token/dvp/dlog/out/
	rm -rf ./integration/token/dvp/fabtoken/out/
	rm -rf ./integration/token/interop/fabtoken/out/
	rm -rf ./integration/token/interop/dlog/out/
	rm -rf ./integration/token/fungible/update/out/
	rm -rf ./integration/token/fungible/update/testdata/

.PHONY: clean-fabric-peer-images
# clean up fabric peer images
clean-fabric-peer-images:
	docker images -a | grep "_peer.org" | awk '{print $3}' | xargs docker rmi
	docker images -a | grep "_peer_" | awk '{print $3}' | xargs docker rmi

.PHONY: tokengen
# install tokengen tool (must build without cgo; see #1445)
tokengen:
	@cd ./cmd/tokengen/; CGO_ENABLED=0 go install github.com/LFDT-Panurus/panurus/cmd/tokengen

.PHONY: artifactgen
# install artifactgen tool (must build without cgo; see #1445)
artifactgen:
	@cd ./cmd/artifactgen/; CGO_ENABLED=0 go install github.com/LFDT-Panurus/panurus/cmd/artifactgen

.PHONY: skicleanup
# install skicleanup tool (must build without cgo; see #1445)
skicleanup:
	@cd ./cmd/skicleanup/; CGO_ENABLED=0 go install github.com/LFDT-Panurus/panurus/cmd/skicleanup

.PHONY: traceinspector
# install traceinspector tool
traceinspector:
	@go install ./token/services/benchmark/cmd/traceinspector

.PHONY: memcheck
# install memcheck tool
memcheck:
	@go install ./token/services/benchmark/cmd/memcheck

.PHONY: txgen
# install idemixgen/txgen tool
txgen:
	@go install github.com/IBM/idemix/tools/idemixgen

.PHONY: profile-validator-transfer
# regenerate validator transfer profile documentation
profile-validator-transfer:
	@echo "Regenerating validator transfer profile..."
	@cd tools/profiler && ./profile.sh BenchmarkValidatorTransfer -f VerifyTokenRequestFromRaw 

.PHONY: profile
# run profiler on any test or benchmark
# Usage: make profile TEST=BenchmarkValidatorTransfer [ROOT=VerifyTokenRequestFromRaw]
# Usage: make profile TEST=TestMyFunction
profile:
	@if [ -z "$(TEST)" ]; then \
		echo "Error: TEST parameter is required"; \
		echo "Usage: make profile TEST=<test_or_benchmark_name> [ROOT=<root_function>]"; \
		echo "Example: make profile TEST=BenchmarkValidatorTransfer ROOT=VerifyTokenRequestFromRaw"; \
		exit 1; \
	fi
	@echo "Running profiler on $(TEST)..."
	@if [ -n "$(ROOT)" ]; then \
		cd tools/profiler && ./profile.sh $(TEST) -f $(ROOT); \
	else \
		cd tools/profiler && ./profile.sh $(TEST); \
	fi

.PHONY: clean-all-containers
# clean up all docker containers
clean-all-containers:
	@if [ -n "$$(docker ps -aq)" ]; then docker rm -f $$(docker ps -aq); else echo "No containers to remove"; fi

.PHONY: lint
# run various linters
lint:
	@echo "Running Go Linters..."
	@for dir in $(GO_MODULES); do \
		echo "  Linting module: $$dir"; \
		(cd $$dir && golangci-lint run --color=always --timeout=4m ./...) || exit 1; \
	done

.PHONY: lint-auto-fix
# run linters with auto-fix
lint-auto-fix:
	@echo "Running Go Linters with auto-fix..."
	@for dir in $(GO_MODULES); do \
		echo "  Linting module: $$dir"; \
		(cd $$dir && golangci-lint run --color=always --timeout=4m --fix ./...) || exit 1; \
	done

.PHONY: install-linter-tool
# install golangci-lint
install-linter-tool:
	@echo "Installing golangci Linter"
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(HOME)/go/bin v2.12.2

.PHONY: fmt
fmt: ## Run gofmt on the entire project
	@echo "Running gofmt..."
	@for dir in $(GO_MODULES); do \
		echo "  Formatting module: $$dir"; \
		(cd $$dir && find . -path './.git' -prune -o -name '*.go' -print | xargs gofmt -l -s -w); \
	done

.PHONY: update-all-deps-latest
update-all-deps-latest: ## Update all dependencies in all Go modules to their latest version
	@echo "Updating all dependencies to @latest..."
	@for dir in $$(find . -name "go.mod" -exec dirname {} \;); do \
		echo "=> Updating dependencies in $$dir"; \
		(cd $$dir && go get ./...@latest && go mod tidy); \
	done

.PHONY: docs-install
# Install documentation dependencies
docs-install:
	pip install -r requirements.txt

.PHONY: docs-serve
# Serve documentation locally for development
docs-serve:
	mkdocs serve

.PHONY: docs-build
# Build the static documentation site for production
docs-build:
	mkdocs build --strict

.PHONY: protos-format
protos-format: ## Run buf format to fix protobuf files
	@echo "Fixing protobuf formatting..."
	@buf format -w

.PHONY: protos
# generate protobuf files
protos:
	@echo "Generating protobuf files..."
	@buf generate
