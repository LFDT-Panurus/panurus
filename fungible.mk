.PHONY: integration-tests-dlog-fabric-t1
integration-tests-dlog-fabric-t1:
	make integration-tests-dlog-fabric TEST_FILTER="T1"

.PHONY: integration-tests-dlog-fabric-t2
integration-tests-dlog-fabric-t2:
	make integration-tests-dlog-fabric TEST_FILTER="T2"

.PHONY: integration-tests-dlog-fabric-t2.1
integration-tests-dlog-fabric-t2.1:
	make integration-tests-dlog-fabric TEST_FILTER="T2.1"

.PHONY: integration-tests-dlog-fabric-t3
integration-tests-dlog-fabric-t3:
	make integration-tests-dlog-fabric TEST_FILTER="T3"

.PHONY: integration-tests-dlog-fabric-t4
integration-tests-dlog-fabric-t4:
	make integration-tests-dlog-fabric TEST_FILTER="T4"

.PHONY: integration-tests-dlog-fabric-t5
integration-tests-dlog-fabric-t5:
	make integration-tests-dlog-fabric TEST_FILTER="T5"

.PHONY: integration-tests-dlog-fabric-t6
integration-tests-dlog-fabric-t6:
	make integration-tests-dlog-fabric TEST_FILTER="T6"

.PHONY: integration-tests-dlog-fabric-t7
integration-tests-dlog-fabric-t7:
	make integration-tests-dlog-fabric TEST_FILTER="T7"

.PHONY: integration-tests-dlog-fabric-t8
integration-tests-dlog-fabric-t8:
	make integration-tests-dlog-fabric TEST_FILTER="T8"

.PHONY: integration-tests-dlog-fabric-t9
integration-tests-dlog-fabric-t9:
	make integration-tests-dlog-fabric TEST_FILTER="T9"

.PHONY: integration-tests-dlog-fabric-t10
integration-tests-dlog-fabric-t10:
	make integration-tests-dlog-fabric TEST_FILTER="T10"

.PHONY: integration-tests-dlog-fabric-t11
integration-tests-dlog-fabric-t11:
	make integration-tests-dlog-fabric TEST_FILTER="T11"

.PHONY: integration-tests-dlog-fabric-t12
integration-tests-dlog-fabric-t12:
	make integration-tests-dlog-fabric TEST_FILTER="T12"

.PHONY: integration-tests-dlog-fabric-t13
integration-tests-dlog-fabric-t13:
	make integration-tests-dlog-fabric TEST_FILTER="T13"

.PHONY: integration-tests-dlog-fabric
integration-tests-dlog-fabric:
	cd ./integration/token/fungible/dlog; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-fabtoken-dlog-fabric
integration-tests-fabtoken-dlog-fabric:
	cd ./integration/token/fungible/mixed; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) .

.PHONY: integration-tests-dloghsm-fabric-t1
integration-tests-dloghsm-fabric-t1:
	make integration-tests-dloghsm-fabric TEST_FILTER="T1"

.PHONY: integration-tests-dloghsm-fabric-t2
integration-tests-dloghsm-fabric-t2:
	make integration-tests-dloghsm-fabric TEST_FILTER="T2"

.PHONY: integration-tests-dloghsm-fabric
integration-tests-dloghsm-fabric: install-softhsm
	@echo "Setup SoftHSM"
	@./ci/scripts/setup_softhsm.sh
	@echo "Start Integration Test"
	cd ./integration/token/fungible/dloghsm; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --tags pkcs11 --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-fabtoken-fabric-t1
integration-tests-fabtoken-fabric-t1:
	make integration-tests-fabtoken-fabric TEST_FILTER="T1"

.PHONY: integration-tests-fabtoken-fabric-t2
integration-tests-fabtoken-fabric-t2:
	make integration-tests-fabtoken-fabric TEST_FILTER="T2"

.PHONY: integration-tests-fabtoken-fabric-t3
integration-tests-fabtoken-fabric-t3:
	make integration-tests-fabtoken-fabric TEST_FILTER="T3"

.PHONY: integration-tests-fabtoken-fabric-t4
integration-tests-fabtoken-fabric-t4:
	make integration-tests-fabtoken-fabric TEST_FILTER="T4"

.PHONY: integration-tests-fabtoken-fabric-t5
integration-tests-fabtoken-fabric-t5:
	make integration-tests-fabtoken-fabric TEST_FILTER="T5"

.PHONY: integration-tests-fabtoken-fabric
integration-tests-fabtoken-fabric:
	cd ./integration/token/fungible/fabtoken; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --tags pkcs11 --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-update-t1
integration-tests-update-t1:
	make integration-tests-update TEST_FILTER="T1"

.PHONY: integration-tests-update-t2
integration-tests-update-t2:
	make integration-tests-update TEST_FILTER="T2"

.PHONY: integration-tests-update-t3
integration-tests-update-t3:
	make integration-tests-update TEST_FILTER="T3"

.PHONY: integration-tests-update
integration-tests-update:
	cd ./integration/token/fungible/update; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-dlogstress-t1
integration-tests-dlogstress-t1:
	make integration-tests-dlogstress TEST_FILTER="T1"

.PHONY: integration-tests-dlogstress-t2
integration-tests-dlogstress-t2:
	make integration-tests-dlogstress TEST_FILTER="T2"

.PHONY: integration-tests-dlogstress
integration-tests-dlogstress:
	cd ./integration/token/fungible/dlogstress; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .
