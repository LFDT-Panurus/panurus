.PHONY: chaguar-docker-images
chaguar-docker-images: ## Pull Chaguar (fabric-x) images
	docker pull ghcr.io/hyperledger/fabric-x-committer-test-node:$(FABRIC_X_COMMITTER_VERSION)
	docker tag ghcr.io/hyperledger/fabric-x-committer-test-node:$(FABRIC_X_COMMITTER_VERSION) hyperledger/fabric-x-committer-test-node:$(FABRIC_X_COMMITTER_VERSION)

# The fabric-x tools live in their own subdirectory of $(FAB_BINS): FSC's
# Chaguar network (nwo/fabricx/network.New) sets BinSubdir = "fabric-x" so it
# looks up fxconfig/configtxgen there instead of at the top level of FAB_BINS.
FAB_BINS_FABRIC_X = $(FAB_BINS)/fabric-x

.PHONY: fxconfig
fxconfig: ## Install fxconfig
	@mkdir -p $(FAB_BINS_FABRIC_X)
	@env GOBIN=$(FAB_BINS_FABRIC_X) go install $(GO_FLAGS) github.com/hyperledger/fabric-x/tools/fxconfig@$(FABRIC_X_TOOLS_VERSION)

.PHONY: configtxgen
configtxgen: ## Install configtxgen
	@mkdir -p $(FAB_BINS_FABRIC_X)
	@env GOBIN=$(FAB_BINS_FABRIC_X) go install $(GO_FLAGS) github.com/hyperledger/fabric-x/tools/configtxgen@$(FABRIC_X_TOOLS_VERSION)

# See the note in fungible.mk: these suites loop over integration.AllTestTypes, so
# the specs calling fungible.TestAll also get per-infra targets that CI runs as
# parallel jobs instead of three sequential configurations in one job.
.PHONY: integration-tests-chaguar-dlog-t1
integration-tests-chaguar-dlog-t1:
	make integration-tests-chaguar-dlog TEST_FILTER="T1"

.PHONY: integration-tests-chaguar-dlog-t1-websocket
integration-tests-chaguar-dlog-t1-websocket:
	make integration-tests-chaguar-dlog TEST_FILTER="T1 && websocket"

.PHONY: integration-tests-chaguar-dlog-t1-libp2p
integration-tests-chaguar-dlog-t1-libp2p:
	make integration-tests-chaguar-dlog TEST_FILTER="T1 && libp2p"

.PHONY: integration-tests-chaguar-dlog-t1-replicas
integration-tests-chaguar-dlog-t1-replicas:
	make integration-tests-chaguar-dlog TEST_FILTER="T1 && replicas"

.PHONY: integration-tests-chaguar-dlog-t2
integration-tests-chaguar-dlog-t2:
	make integration-tests-chaguar-dlog TEST_FILTER="T2"

.PHONY: integration-tests-chaguar-dlog-t2.1
integration-tests-chaguar-dlog-t2.1:
	make integration-tests-chaguar-dlog TEST_FILTER="T2.1"

.PHONY: integration-tests-chaguar-dlog-t3
integration-tests-chaguar-dlog-t3:
	make integration-tests-chaguar-dlog TEST_FILTER="T3"

.PHONY: integration-tests-chaguar-dlog-t4
integration-tests-chaguar-dlog-t4:
	make integration-tests-chaguar-dlog TEST_FILTER="T4"

.PHONY: integration-tests-chaguar-dlog-t5
integration-tests-chaguar-dlog-t5:
	make integration-tests-chaguar-dlog TEST_FILTER="T5"

.PHONY: integration-tests-chaguar-dlog-t6
integration-tests-chaguar-dlog-t6:
	make integration-tests-chaguar-dlog TEST_FILTER="T6"

.PHONY: integration-tests-chaguar-dlog-t6-websocket
integration-tests-chaguar-dlog-t6-websocket:
	make integration-tests-chaguar-dlog TEST_FILTER="T6 && websocket"

.PHONY: integration-tests-chaguar-dlog-t6-libp2p
integration-tests-chaguar-dlog-t6-libp2p:
	make integration-tests-chaguar-dlog TEST_FILTER="T6 && libp2p"

.PHONY: integration-tests-chaguar-dlog-t6-replicas
integration-tests-chaguar-dlog-t6-replicas:
	make integration-tests-chaguar-dlog TEST_FILTER="T6 && replicas"

.PHONY: integration-tests-chaguar-dlog-t7
integration-tests-chaguar-dlog-t7:
	make integration-tests-chaguar-dlog TEST_FILTER="T7"

.PHONY: integration-tests-chaguar-dlog-t9
integration-tests-chaguar-dlog-t9:
	make integration-tests-chaguar-dlog TEST_FILTER="T9"

.PHONY: integration-tests-chaguar-dlog-t11
integration-tests-chaguar-dlog-t11:
	make integration-tests-chaguar-dlog TEST_FILTER="T11"

.PHONY: integration-tests-chaguar-dlog-t12
integration-tests-chaguar-dlog-t12:
	make integration-tests-chaguar-dlog TEST_FILTER="T12"

.PHONY: integration-tests-chaguar-dlog-t13
integration-tests-chaguar-dlog-t13:
	make integration-tests-chaguar-dlog TEST_FILTER="T13"

.PHONY: integration-tests-chaguar-dlog-t14
integration-tests-chaguar-dlog-t14:
	make integration-tests-chaguar-dlog TEST_FILTER="T14"

.PHONY: integration-tests-chaguar-dlog-t16
integration-tests-chaguar-dlog-t16:
	make integration-tests-chaguar-dlog TEST_FILTER="T16"

.PHONY: integration-tests-chaguar-dlog-t16-websocket
integration-tests-chaguar-dlog-t16-websocket:
	make integration-tests-chaguar-dlog TEST_FILTER="T16 && websocket"

.PHONY: integration-tests-chaguar-dlog-t16-libp2p
integration-tests-chaguar-dlog-t16-libp2p:
	make integration-tests-chaguar-dlog TEST_FILTER="T16 && libp2p"

.PHONY: integration-tests-chaguar-dlog-t16-replicas
integration-tests-chaguar-dlog-t16-replicas:
	make integration-tests-chaguar-dlog TEST_FILTER="T16 && replicas"

# The dlogx "Extras" container (T12, T13, T4, T5, T3) shares one network per infra type.
# The filter is what CI runs; T12, T2.1 and T2 are disabled (#1945): widen it to enable them.
CHAGUAR_DLOG_EXTRAS_FILTER = (T3 || T4 || T5 || T13)

.PHONY: integration-tests-chaguar-dlog-extras
integration-tests-chaguar-dlog-extras:
	make integration-tests-chaguar-dlog TEST_FILTER="$(CHAGUAR_DLOG_EXTRAS_FILTER)"

.PHONY: integration-tests-chaguar-dlog-extras-websocket
integration-tests-chaguar-dlog-extras-websocket:
	make integration-tests-chaguar-dlog TEST_FILTER="$(CHAGUAR_DLOG_EXTRAS_FILTER) && websocket"

.PHONY: integration-tests-chaguar-dlog-extras-libp2p
integration-tests-chaguar-dlog-extras-libp2p:
	make integration-tests-chaguar-dlog TEST_FILTER="$(CHAGUAR_DLOG_EXTRAS_FILTER) && libp2p"

.PHONY: integration-tests-chaguar-dlog-extras-replicas
integration-tests-chaguar-dlog-extras-replicas:
	make integration-tests-chaguar-dlog TEST_FILTER="$(CHAGUAR_DLOG_EXTRAS_FILTER) && replicas"

.PHONY: integration-tests-chaguar-dlog
integration-tests-chaguar-dlog:
	cd ./integration/token/fungible/dlogx; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .
