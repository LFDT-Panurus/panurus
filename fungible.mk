# The suites below wrap their specs in a loop over integration.AllTestTypes, so a
# label filter of just "TN" runs that spec once per infrastructure type
# (websocket, libp2p, replicas) sequentially in a single job. For the specs that
# call fungible.TestAll — by far the most expensive — we also expose per-infra
# targets so CI can run the three configurations as parallel jobs instead. The
# aggregate "TN" targets are kept for local runs and still cover all three.
.PHONY: integration-tests-dlog-fabric-t1
integration-tests-dlog-fabric-t1:
	make integration-tests-dlog-fabric TEST_FILTER="T1"

.PHONY: integration-tests-dlog-fabric-t1-websocket
integration-tests-dlog-fabric-t1-websocket:
	make integration-tests-dlog-fabric TEST_FILTER="T1 && websocket"

.PHONY: integration-tests-dlog-fabric-t1-libp2p
integration-tests-dlog-fabric-t1-libp2p:
	make integration-tests-dlog-fabric TEST_FILTER="T1 && libp2p"

.PHONY: integration-tests-dlog-fabric-t1-replicas
integration-tests-dlog-fabric-t1-replicas:
	make integration-tests-dlog-fabric TEST_FILTER="T1 && replicas"

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

.PHONY: integration-tests-dlog-fabric-t6-websocket
integration-tests-dlog-fabric-t6-websocket:
	make integration-tests-dlog-fabric TEST_FILTER="T6 && websocket"

.PHONY: integration-tests-dlog-fabric-t6-libp2p
integration-tests-dlog-fabric-t6-libp2p:
	make integration-tests-dlog-fabric TEST_FILTER="T6 && libp2p"

.PHONY: integration-tests-dlog-fabric-t6-replicas
integration-tests-dlog-fabric-t6-replicas:
	make integration-tests-dlog-fabric TEST_FILTER="T6 && replicas"

.PHONY: integration-tests-dlog-fabric-t7
integration-tests-dlog-fabric-t7:
	make integration-tests-dlog-fabric TEST_FILTER="T7"

.PHONY: integration-tests-dlog-fabric-t8
integration-tests-dlog-fabric-t8:
	make integration-tests-dlog-fabric TEST_FILTER="T8"

.PHONY: integration-tests-dlog-fabric-t8-websocket
integration-tests-dlog-fabric-t8-websocket:
	make integration-tests-dlog-fabric TEST_FILTER="T8 && websocket"

.PHONY: integration-tests-dlog-fabric-t8-libp2p
integration-tests-dlog-fabric-t8-libp2p:
	make integration-tests-dlog-fabric TEST_FILTER="T8 && libp2p"

.PHONY: integration-tests-dlog-fabric-t8-replicas
integration-tests-dlog-fabric-t8-replicas:
	make integration-tests-dlog-fabric TEST_FILTER="T8 && replicas"

.PHONY: integration-tests-dlog-fabric-t9
integration-tests-dlog-fabric-t9:
	make integration-tests-dlog-fabric TEST_FILTER="T9"

.PHONY: integration-tests-dlog-fabric-t10
integration-tests-dlog-fabric-t10:
	make integration-tests-dlog-fabric TEST_FILTER="T10"

.PHONY: integration-tests-dlog-fabric-t10-websocket
integration-tests-dlog-fabric-t10-websocket:
	make integration-tests-dlog-fabric TEST_FILTER="T10 && websocket"

.PHONY: integration-tests-dlog-fabric-t10-libp2p
integration-tests-dlog-fabric-t10-libp2p:
	make integration-tests-dlog-fabric TEST_FILTER="T10 && libp2p"

.PHONY: integration-tests-dlog-fabric-t10-replicas
integration-tests-dlog-fabric-t10-replicas:
	make integration-tests-dlog-fabric TEST_FILTER="T10 && replicas"

.PHONY: integration-tests-dlog-fabric-t11
integration-tests-dlog-fabric-t11:
	make integration-tests-dlog-fabric TEST_FILTER="T11"

.PHONY: integration-tests-dlog-fabric-t12
integration-tests-dlog-fabric-t12:
	make integration-tests-dlog-fabric TEST_FILTER="T12"

.PHONY: integration-tests-dlog-fabric-t13
integration-tests-dlog-fabric-t13:
	make integration-tests-dlog-fabric TEST_FILTER="T13"

# T12 and T13 share one network per infrastructure type (see the "Multisig and Redeem to
# yourself" container in integration/token/fungible/dlog), so CI runs them in one job.
.PHONY: integration-tests-dlog-fabric-t12-t13
integration-tests-dlog-fabric-t12-t13:
	make integration-tests-dlog-fabric TEST_FILTER="T12 || T13"

.PHONY: integration-tests-dlog-fabric-t14
integration-tests-dlog-fabric-t14:
	make integration-tests-dlog-fabric TEST_FILTER="T14"

.PHONY: integration-tests-dlog-fabric-t15
integration-tests-dlog-fabric-t15:
	make integration-tests-dlog-fabric TEST_FILTER="T15"

.PHONY: integration-tests-dlog-fabric-t17
integration-tests-dlog-fabric-t17:
	make integration-tests-dlog-fabric TEST_FILTER="T17"

# T1, T17, T4, T5, T3, T2.1 and T2 share one network per infrastructure type (see the
# "T1 Fungible with Auditor ne Issuer and extras" container in
# integration/token/fungible/dlog), and T6 and T7 another one (the "Fungible with
# Auditor = Issuer" container), so CI runs each group as one job per infrastructure type.
DLOG_FABRIC_ARIES_FILTER = (T1 || T2 || T2.1 || T3 || T4 || T5 || T17)
DLOG_FABRIC_T6_T7_FILTER = (T6 || T7)

.PHONY: integration-tests-dlog-fabric-aries-websocket
integration-tests-dlog-fabric-aries-websocket:
	make integration-tests-dlog-fabric TEST_FILTER="$(DLOG_FABRIC_ARIES_FILTER) && websocket"

.PHONY: integration-tests-dlog-fabric-aries-libp2p
integration-tests-dlog-fabric-aries-libp2p:
	make integration-tests-dlog-fabric TEST_FILTER="$(DLOG_FABRIC_ARIES_FILTER) && libp2p"

.PHONY: integration-tests-dlog-fabric-aries-replicas
integration-tests-dlog-fabric-aries-replicas:
	make integration-tests-dlog-fabric TEST_FILTER="$(DLOG_FABRIC_ARIES_FILTER) && replicas"

.PHONY: integration-tests-dlog-fabric-t6-t7-websocket
integration-tests-dlog-fabric-t6-t7-websocket:
	make integration-tests-dlog-fabric TEST_FILTER="$(DLOG_FABRIC_T6_T7_FILTER) && websocket"

.PHONY: integration-tests-dlog-fabric-t6-t7-libp2p
integration-tests-dlog-fabric-t6-t7-libp2p:
	make integration-tests-dlog-fabric TEST_FILTER="$(DLOG_FABRIC_T6_T7_FILTER) && libp2p"

.PHONY: integration-tests-dlog-fabric-t6-t7-replicas
integration-tests-dlog-fabric-t6-t7-replicas:
	make integration-tests-dlog-fabric TEST_FILTER="$(DLOG_FABRIC_T6_T7_FILTER) && replicas"

.PHONY: integration-tests-dlog-fabric
integration-tests-dlog-fabric:
	cd ./integration/token/fungible/dlog; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-fabtoken-dlog-fabric
integration-tests-fabtoken-dlog-fabric:
	cd ./integration/token/fungible/mixed; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) .

.PHONY: integration-tests-dloghsm-fabric-t1
integration-tests-dloghsm-fabric-t1:
	make integration-tests-dloghsm-fabric TEST_FILTER="T1"

.PHONY: integration-tests-dloghsm-fabric-t1-websocket
integration-tests-dloghsm-fabric-t1-websocket:
	make integration-tests-dloghsm-fabric TEST_FILTER="T1 && websocket"

.PHONY: integration-tests-dloghsm-fabric-t1-libp2p
integration-tests-dloghsm-fabric-t1-libp2p:
	make integration-tests-dloghsm-fabric TEST_FILTER="T1 && libp2p"

.PHONY: integration-tests-dloghsm-fabric-t1-replicas
integration-tests-dloghsm-fabric-t1-replicas:
	make integration-tests-dloghsm-fabric TEST_FILTER="T1 && replicas"

.PHONY: integration-tests-dloghsm-fabric-t2
integration-tests-dloghsm-fabric-t2:
	make integration-tests-dloghsm-fabric TEST_FILTER="T2"

.PHONY: integration-tests-dloghsm-fabric-t2-websocket
integration-tests-dloghsm-fabric-t2-websocket:
	make integration-tests-dloghsm-fabric TEST_FILTER="T2 && websocket"

.PHONY: integration-tests-dloghsm-fabric-t2-libp2p
integration-tests-dloghsm-fabric-t2-libp2p:
	make integration-tests-dloghsm-fabric TEST_FILTER="T2 && libp2p"

.PHONY: integration-tests-dloghsm-fabric-t2-replicas
integration-tests-dloghsm-fabric-t2-replicas:
	make integration-tests-dloghsm-fabric TEST_FILTER="T2 && replicas"

.PHONY: integration-tests-dloghsm-fabric
integration-tests-dloghsm-fabric: install-softhsm
	@echo "Setup SoftHSM"
	@./ci/scripts/setup_softhsm.sh
	@echo "Start Integration Test"
	cd ./integration/token/fungible/dloghsm; export FAB_BINS=$(FAB_BINS); ginkgo $(GINKGO_TEST_OPTS) --tags pkcs11 --label-filter="$(TEST_FILTER)" .

.PHONY: integration-tests-fabtoken-fabric-t1
integration-tests-fabtoken-fabric-t1:
	make integration-tests-fabtoken-fabric TEST_FILTER="T1"

.PHONY: integration-tests-fabtoken-fabric-t1-websocket
integration-tests-fabtoken-fabric-t1-websocket:
	make integration-tests-fabtoken-fabric TEST_FILTER="T1 && websocket"

.PHONY: integration-tests-fabtoken-fabric-t1-libp2p
integration-tests-fabtoken-fabric-t1-libp2p:
	make integration-tests-fabtoken-fabric TEST_FILTER="T1 && libp2p"

.PHONY: integration-tests-fabtoken-fabric-t1-replicas
integration-tests-fabtoken-fabric-t1-replicas:
	make integration-tests-fabtoken-fabric TEST_FILTER="T1 && replicas"

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

.PHONY: integration-tests-fabtoken-fabric-t17
integration-tests-fabtoken-fabric-t17:
	make integration-tests-fabtoken-fabric TEST_FILTER="T17"

# T1, T17, T4, T5, T3 and T2 share one network per infrastructure type (see the "Fungible"
# container in integration/token/fungible/fabtoken), so CI runs them as one job per
# infrastructure type.
FABTOKEN_FABRIC_ALL_FILTER = (T1 || T2 || T3 || T4 || T5 || T17)

.PHONY: integration-tests-fabtoken-fabric-all-websocket
integration-tests-fabtoken-fabric-all-websocket:
	make integration-tests-fabtoken-fabric TEST_FILTER="$(FABTOKEN_FABRIC_ALL_FILTER) && websocket"

.PHONY: integration-tests-fabtoken-fabric-all-libp2p
integration-tests-fabtoken-fabric-all-libp2p:
	make integration-tests-fabtoken-fabric TEST_FILTER="$(FABTOKEN_FABRIC_ALL_FILTER) && libp2p"

.PHONY: integration-tests-fabtoken-fabric-all-replicas
integration-tests-fabtoken-fabric-all-replicas:
	make integration-tests-fabtoken-fabric TEST_FILTER="$(FABTOKEN_FABRIC_ALL_FILTER) && replicas"

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

.PHONY: integration-tests-update-t4
integration-tests-update-t4:
	make integration-tests-update TEST_FILTER="T4"

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
