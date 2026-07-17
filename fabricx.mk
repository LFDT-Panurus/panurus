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

.PHONY: integration-tests-fabricx-dlog-t2
integration-tests-fabricx-dlog-t2:
	make integration-tests-fabricx-dlog TEST_FILTER="T2"

.PHONY: integration-tests-fabricx-dlog-t2.1
integration-tests-fabricx-dlog-t2.1:
	make integration-tests-fabricx-dlog TEST_FILTER="T2.1"

.PHONY: integration-tests-fabricx-dlog-t3
integration-tests-fabricx-dlog-t3:
	make integration-tests-fabricx-dlog TEST_FILTER="T3"

.PHONY: integration-tests-fabricx-dlog-t4
integration-tests-fabricx-dlog-t4:
	make integration-tests-fabricx-dlog TEST_FILTER="T4"

.PHONY: integration-tests-fabricx-dlog-t5
integration-tests-fabricx-dlog-t5:
	make integration-tests-fabricx-dlog TEST_FILTER="T5"

.PHONY: integration-tests-fabricx-dlog-t6
integration-tests-fabricx-dlog-t6:
	make integration-tests-fabricx-dlog TEST_FILTER="T6"

.PHONY: integration-tests-fabricx-dlog-t7
integration-tests-fabricx-dlog-t7:
	make integration-tests-fabricx-dlog TEST_FILTER="T7"

.PHONY: integration-tests-fabricx-dlog-t9
integration-tests-fabricx-dlog-t9:
	make integration-tests-fabricx-dlog TEST_FILTER="T9"

.PHONY: integration-tests-fabricx-dlog-t11
integration-tests-fabricx-dlog-t11:
	make integration-tests-fabricx-dlog TEST_FILTER="T11"

.PHONY: integration-tests-fabricx-dlog-t12
integration-tests-fabricx-dlog-t12:
	make integration-tests-fabricx-dlog TEST_FILTER="T12"

.PHONY: integration-tests-fabricx-dlog-t13
integration-tests-fabricx-dlog-t13:
	make integration-tests-fabricx-dlog TEST_FILTER="T13"

.PHONY: integration-tests-fabricx-dlog-t14
integration-tests-fabricx-dlog-t14:
	make integration-tests-fabricx-dlog TEST_FILTER="T14"

.PHONY: integration-tests-fabricx-dlog-t16
integration-tests-fabricx-dlog-t16:
	make integration-tests-fabricx-dlog TEST_FILTER="T16"

.PHONY: integration-tests-fabricx-dlog
integration-tests-fabricx-dlog:
	cd ./integration/token/fungible/dlogx; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .
