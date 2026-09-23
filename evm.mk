.PHONY: besu-docker-images
# pull the besu docker image the EVM integration suites run against
besu-docker-images:
	docker pull $(BESU_IMAGE)

.PHONY: fabricx-evm-docker-images
# pull the fabric-x-evm docker image the EVM gateway integration suite runs against
fabricx-evm-docker-images:
	docker pull $(FABRICX_EVM_IMAGE)

.PHONY: integration-tests-evm
# run the fungible integration tests against an EVM backend (Besu).
# Unlike the fabric suites this needs no FAB_BINS, but it does need docker and forge.
integration-tests-evm: besu-docker-images
	cd ./integration/token/fungible/evm; ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-evm-t1
integration-tests-evm-t1:
	make integration-tests-evm TEST_FILTER="T1"

.PHONY: integration-tests-evm-t2
integration-tests-evm-t2:
	make integration-tests-evm TEST_FILTER="T2"

.PHONY: integration-tests-evm-t2.1
integration-tests-evm-t2.1:
	make integration-tests-evm TEST_FILTER="T2.1"

.PHONY: integration-tests-evm-t6
integration-tests-evm-t6:
	make integration-tests-evm TEST_FILTER="T6"

.PHONY: integration-tests-evm-t9
integration-tests-evm-t9:
	make integration-tests-evm TEST_FILTER="T9"

.PHONY: integration-tests-evm-t11
integration-tests-evm-t11:
	make integration-tests-evm TEST_FILTER="T11"

.PHONY: integration-tests-evm-t12
integration-tests-evm-t12:
	make integration-tests-evm TEST_FILTER="T12"

.PHONY: integration-tests-evm-t13
integration-tests-evm-t13:
	make integration-tests-evm TEST_FILTER="T13"

.PHONY: integration-tests-evm-fabtoken
# run the fungible integration tests against an EVM backend with the fabtoken driver.
integration-tests-evm-fabtoken: besu-docker-images
	cd ./integration/token/fungible/evmfabtoken; ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-evm-fabtoken-t1
integration-tests-evm-fabtoken-t1:
	make integration-tests-evm-fabtoken TEST_FILTER="T1"

.PHONY: integration-tests-evm-fabtoken-t2
integration-tests-evm-fabtoken-t2:
	make integration-tests-evm-fabtoken TEST_FILTER="T2"

.PHONY: integration-tests-evm-fabtoken-t6
integration-tests-evm-fabtoken-t6:
	make integration-tests-evm-fabtoken TEST_FILTER="T6"

.PHONY: integration-tests-evm-fabtoken-t9
integration-tests-evm-fabtoken-t9:
	make integration-tests-evm-fabtoken TEST_FILTER="T9"

.PHONY: integration-tests-evm-fabtoken-t11
integration-tests-evm-fabtoken-t11:
	make integration-tests-evm-fabtoken TEST_FILTER="T11"

.PHONY: integration-tests-evm-fabtoken-t13
integration-tests-evm-fabtoken-t13:
	make integration-tests-evm-fabtoken TEST_FILTER="T13"

.PHONY: integration-tests-evm-gateway
# run the fungible integration tests against the fabric-x-evm gateway backend.
integration-tests-evm-gateway: fabricx-evm-docker-images
	cd ./integration/token/fungible/evmgw; ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-evm-gateway-t1
integration-tests-evm-gateway-t1:
	make integration-tests-evm-gateway TEST_FILTER="T1"

.PHONY: integration-tests-evm-gateway-t2
integration-tests-evm-gateway-t2:
	make integration-tests-evm-gateway TEST_FILTER="T2"

.PHONY: integration-tests-evm-gateway-t2.1
integration-tests-evm-gateway-t2.1:
	make integration-tests-evm-gateway TEST_FILTER="T2.1"

.PHONY: integration-tests-evm-gateway-t6
integration-tests-evm-gateway-t6:
	make integration-tests-evm-gateway TEST_FILTER="T6"

.PHONY: integration-tests-evm-gateway-t9
integration-tests-evm-gateway-t9:
	make integration-tests-evm-gateway TEST_FILTER="T9"

.PHONY: integration-tests-evm-gateway-t11
integration-tests-evm-gateway-t11:
	make integration-tests-evm-gateway TEST_FILTER="T11"

.PHONY: integration-tests-evm-gateway-t12
integration-tests-evm-gateway-t12:
	make integration-tests-evm-gateway TEST_FILTER="T12"

.PHONY: integration-tests-evm-gateway-t13
integration-tests-evm-gateway-t13:
	make integration-tests-evm-gateway TEST_FILTER="T13"

.PHONY: integration-tests-evm-gateway-fabtoken
# run the fungible integration tests against the fabric-x-evm gateway backend with the fabtoken driver.
integration-tests-evm-gateway-fabtoken: fabricx-evm-docker-images
	cd ./integration/token/fungible/evmgwfabtoken; ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-evm-gateway-fabtoken-t1
integration-tests-evm-gateway-fabtoken-t1:
	make integration-tests-evm-gateway-fabtoken TEST_FILTER="T1"

.PHONY: integration-tests-evm-gateway-fabtoken-t2
integration-tests-evm-gateway-fabtoken-t2:
	make integration-tests-evm-gateway-fabtoken TEST_FILTER="T2"

.PHONY: integration-tests-evm-gateway-fabtoken-t6
integration-tests-evm-gateway-fabtoken-t6:
	make integration-tests-evm-gateway-fabtoken TEST_FILTER="T6"

.PHONY: integration-tests-evm-gateway-fabtoken-t9
integration-tests-evm-gateway-fabtoken-t9:
	make integration-tests-evm-gateway-fabtoken TEST_FILTER="T9"

.PHONY: integration-tests-evm-gateway-fabtoken-t11
integration-tests-evm-gateway-fabtoken-t11:
	make integration-tests-evm-gateway-fabtoken TEST_FILTER="T11"

.PHONY: integration-tests-evm-gateway-fabtoken-t13
integration-tests-evm-gateway-fabtoken-t13:
	make integration-tests-evm-gateway-fabtoken TEST_FILTER="T13"
