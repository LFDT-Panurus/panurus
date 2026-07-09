.PHONY: fabricx-docker-images
fabricx-docker-images: ## Pull fabric-x images
	docker pull hyperledger/fabric-x-committer-test-node:$(FABRIC_X_COMMITTER_VERSION)

.PHONY: fxconfig
fxconfig: ## Install fxconfig
	@env GOBIN=$(FAB_BINS) go install $(GO_FLAGS) github.com/hyperledger/fabric-x/tools/fxconfig@$(FABRIC_X_TOOLS_VERSION)

.PHONY: configtxgen
configtxgen: ## Install configtxgen
	@env GOBIN=$(FAB_BINS) go install $(GO_FLAGS) github.com/hyperledger/fabric-x/tools/configtxgen@$(FABRIC_X_TOOLS_VERSION)

.PHONY: integration-tests-fabricx-dlog-t1
integration-tests-fabricx-dlog-t1:
	make integration-tests-fabricx-dlog TEST_FILTER="T1"

.PHONY: integration-tests-fabricx-dlog
integration-tests-fabricx-dlog:
	cd ./integration/token/fungible/dlogx; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .
