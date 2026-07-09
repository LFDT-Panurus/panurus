.PHONY: integration-tests-interop-dlog-t1
integration-tests-interop-dlog-t1:
	make integration-tests-interop-dlog TEST_FILTER="T1"

.PHONY: integration-tests-interop-dlog-t2
integration-tests-interop-dlog-t2:
	make integration-tests-interop-dlog TEST_FILTER="T2"

.PHONY: integration-tests-interop-dlog-t3
integration-tests-interop-dlog-t3:
	make integration-tests-interop-dlog TEST_FILTER="T3"

.PHONY: integration-tests-interop-dlog-t4
integration-tests-interop-dlog-t4:
	make integration-tests-interop-dlog TEST_FILTER="T4"

.PHONY: integration-tests-interop-dlog-t5
integration-tests-interop-dlog-t5:
	make integration-tests-interop-dlog TEST_FILTER="T5"

.PHONY: integration-tests-interop-dlog-t6
integration-tests-interop-dlog-t6:
	make integration-tests-interop-dlog TEST_FILTER="T6"

.PHONY: integration-tests-interop-dlog
integration-tests-interop-dlog:
	cd ./integration/token/interop/dlog; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .


.PHONY: integration-tests-interop-fabtoken-t1
integration-tests-interop-fabtoken-t1:
	make integration-tests-interop-fabtoken TEST_FILTER="T1"

.PHONY: integration-tests-interop-fabtoken-t2
integration-tests-interop-fabtoken-t2:
	make integration-tests-interop-fabtoken TEST_FILTER="T2"

.PHONY: integration-tests-interop-fabtoken-t3
integration-tests-interop-fabtoken-t3:
	make integration-tests-interop-fabtoken TEST_FILTER="T3"

.PHONY: integration-tests-interop-fabtoken-t4
integration-tests-interop-fabtoken-t4:
	make integration-tests-interop-fabtoken TEST_FILTER="T4"

.PHONY: integration-tests-interop-fabtoken-t5
integration-tests-interop-fabtoken-t5:
	make integration-tests-interop-fabtoken TEST_FILTER="T5"

.PHONY: integration-tests-interop-fabtoken-t6
integration-tests-interop-fabtoken-t6:
	make integration-tests-interop-fabtoken TEST_FILTER="T6"

.PHONY: integration-tests-interop-fabtoken
integration-tests-interop-fabtoken:
	cd ./integration/token/interop/fabtoken; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .
