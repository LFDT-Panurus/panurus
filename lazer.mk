# Support for the lazer (post-quantum anonymous credential) identity module in
# x/token/services/identity/lazer. That module is a separate Go module because it
# uses cgo and links a native library, liblazer.a, that is not part of this
# repository. These targets build lazer and install what the module links
# against into $(LAZER_PREFIX), so that `make lazer-test` (or plain `go test` in
# the module) works. The CBDC scheme (anoncreds/anoncreds.c) is part of that module and is compiled
# by cgo against the lazer.h installed here. See x/token/services/identity/lazer/README.md.
#
# Prerequisites: a C/C++ toolchain, make, cmake, unzip, patch, git, GMP and MPFR
# (e.g. `apt-get install build-essential cmake unzip patch git libgmp-dev libmpfr-dev`
# or `brew install cmake gmp mpfr`).
#
# Where the lazer sources come from:
#   (unset)                    LAZER_REPO at the pinned LAZER_COMMIT (the default)
#   LAZER_REF=<branch or tag>  LAZER_REPO at that ref instead of the pinned commit
#   LAZER_SRC=/path/to/lazer   copy of a local checkout (it is never built in place)
# Either way, the Panurus-local patches in $(LAZER_PATCHES) (described in
# docs/services/identity-lazer.md, "lazer revision")
# are then applied, in lexical order, to the copy in $(LAZER_BUILD); the
# LAZER_SRC checkout itself is never modified. A patch that does not apply
# exactly (no fuzz, not already applied) fails the build. The revision that
# was built and the applied patches with their sha256 are recorded in
# $(LAZER_PREFIX)/REVISION.
#
# Examples:
#   make lazer-install LAZER_SRC=$$HOME/go/src/github.com/lazer-crypto/lazer
#   make lazer-test

LAZER_MODULE ?= x/token/services/identity/lazer
# The cgo directives of the anoncreds package point at this directory (relative to the
# package), so it is not configurable.
LAZER_PREFIX := $(CURDIR)/.lazer
LAZER_BUILD  := $(LAZER_PREFIX)/build
# Pinned lazer revision. It carries the arm64/macOS support and the int_mod
# fix of lazer-crypto/lazer#33, so until that PR is merged it is fetched from
# the fork it was opened from. A commit that only exists on a fork can become
# unfetchable if that branch is force-pushed or deleted, so once the PR is
# merged point LAZER_REPO back to https://github.com/lazer-crypto/lazer.git and
# LAZER_COMMIT to the merge commit.
LAZER_REPO   ?= https://github.com/adecaro/lazer.git
LAZER_COMMIT ?= cc536f79c73a1cce6c8de5120564b15c809d1caa
LAZER_REF    ?=
LAZER_SRC    ?=
# C flags for lazer. Empty means: use lazer's own Makefile defaults (-O3, the
# architecture flags and the Falcon defines). Do not override them lightly:
# lazer's tests/abdlop-test fails its randomized check at line 146 in about 90%
# of the runs when built with -O2, against a few percent with the default -O3.
LAZER_CFLAGS ?=
# Panurus-local fixes to lazer, applied by lazer-fetch. They are part of the
# input of the build, so the CI cache key of .lazer includes them.
LAZER_PATCHES := $(CURDIR)/$(LAZER_MODULE)/patches
LAZER_SHA256  := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo 'shasum -a 256')
LAZER_LIBS    = $(LAZER_PREFIX)/lib/liblazer.a $(LAZER_PREFIX)/lib/libhexl.a $(LAZER_PREFIX)/include/lazer.h $(LAZER_PREFIX)/include/src/moduli.h

.PHONY: lazer-install
# build lazer and install lazer.h, liblazer.a and libhexl.a into $(LAZER_PREFIX)
lazer-install: lazer-fetch
	@echo "Building lazer in $(LAZER_BUILD)"
	@rm -f $(LAZER_BUILD)/liblazer.a $(LAZER_BUILD)/lazer.h
	$(MAKE) -C $(LAZER_BUILD) liblazer.a lazer.h $(if $(strip $(LAZER_CFLAGS)),CFLAGS="$(LAZER_CFLAGS)")
	@mkdir -p $(LAZER_PREFIX)/include/src $(LAZER_PREFIX)/lib
	cp $(LAZER_BUILD)/lazer.h $(LAZER_PREFIX)/include/lazer.h
	@# lazer.h includes "src/moduli.h" relative to itself
	cp $(LAZER_BUILD)/src/moduli.h $(LAZER_PREFIX)/include/src/moduli.h
	cp $(LAZER_BUILD)/liblazer.a $(LAZER_PREFIX)/lib/liblazer.a
	cp "$$(find $(LAZER_BUILD)/third_party/hexl-development/build -name libhexl.a | head -n 1)" $(LAZER_PREFIX)/lib/libhexl.a
	@mv $(LAZER_BUILD)/.lazer-revision $(LAZER_PREFIX)/REVISION
	@cat $(LAZER_PREFIX)/REVISION
	@echo "lazer installed in $(LAZER_PREFIX), run 'make lazer-test'"

.PHONY: lazer-fetch
# get a fresh copy of the lazer sources in $(LAZER_BUILD)
lazer-fetch:
	@rm -rf $(LAZER_BUILD)
	@mkdir -p $(LAZER_PREFIX)
ifeq ($(strip $(LAZER_SRC)),)
	git init -q $(LAZER_BUILD)
	git -C $(LAZER_BUILD) fetch -q --depth 1 $(LAZER_REPO) $(or $(strip $(LAZER_REF)),$(LAZER_COMMIT))
	git -C $(LAZER_BUILD) checkout -q FETCH_HEAD
	@printf 'repo:   %s\nref:    %s\ncommit: %s\n' "$(LAZER_REPO)" "$(or $(strip $(LAZER_REF)),pinned)" \
		"$$(git -C $(LAZER_BUILD) rev-parse HEAD)" > $(LAZER_BUILD)/.lazer-revision
else
	@test -d "$(LAZER_SRC)/src" || { echo "LAZER_SRC=$(LAZER_SRC) is not a lazer checkout"; exit 1; }
	@mkdir -p $(LAZER_BUILD)
	@# copy the sources, but not build products nor the extracted third-party trees
	cd $(LAZER_SRC) && tar -cf - --exclude=.git --exclude='*.o' --exclude=liblazer.a --exclude=liblazer.so \
		--exclude=lazer.h --exclude=third_party/hexl-development --exclude=third_party/Falcon-impl-20211101 . \
		| tar -xf - -C $(LAZER_BUILD)
	@{ printf 'source: %s\n' "$(abspath $(LAZER_SRC))"; \
	   if git -C $(LAZER_SRC) rev-parse -q HEAD >/dev/null 2>&1; then \
	     printf 'commit: %s%s\n' "$$(git -C $(LAZER_SRC) rev-parse HEAD)" \
	       "$$(test -z "$$(git -C $(LAZER_SRC) status --porcelain --untracked-files=no)" || echo ' (with uncommitted changes)')"; \
	   else echo 'commit: unknown (not a git checkout)'; fi; } > $(LAZER_BUILD)/.lazer-revision
endif
	@# apply the Panurus-local patches strictly: no fuzz, no reversed or
	@# already applied patch, no partial application
	@printf 'patches:\n' >> $(LAZER_BUILD)/.lazer-revision
	@set -e; for p in $$(ls $(LAZER_PATCHES)/*.patch 2>/dev/null | LC_ALL=C sort); do \
		echo "Applying $$(basename $$p)"; \
		patch --forward --strip=1 --fuzz=0 --batch --quiet -d $(LAZER_BUILD) -i $$p \
			|| { echo "lazer patch $$(basename $$p) does not apply to $(LAZER_BUILD)"; exit 1; }; \
		printf '  %s  %s\n' "$$($(LAZER_SHA256) < $$p | cut -d' ' -f1)" "$$(basename $$p)" \
			>> $(LAZER_BUILD)/.lazer-revision; \
	done

.PHONY: lazer-check
# check that lazer is installed
lazer-check:
	@for f in $(LAZER_LIBS); do \
		test -f $$f || { echo "$$f not found, run 'make lazer-install' first"; exit 1; }; \
	done
	@if test -f $(LAZER_PREFIX)/REVISION; then sed 's/^/lazer /' $(LAZER_PREFIX)/REVISION; \
	else echo "lazer revision unknown (installed before REVISION was recorded)"; fi

.PHONY: lazer-test
# run the unit tests of the lazer identity module (needs lazer-install)
lazer-test: lazer-check
	cd $(LAZER_MODULE) && go test -race -count=1 -timeout 30m ./...

.PHONY: lazer-test-short
# like lazer-test without the race detector
lazer-test-short: lazer-check
	cd $(LAZER_MODULE) && go test -count=1 -timeout 30m ./...

.PHONY: lazer-bench
# print sizes and timings of the lazer identities
lazer-bench: lazer-check
	cd $(LAZER_MODULE) && go test -run XXX -bench . -benchtime 3x ./

.PHONY: lazer-clean
# remove the installed lazer
lazer-clean:
	rm -rf $(LAZER_PREFIX)/* $(LAZER_PREFIX)/.[!.]* 2>/dev/null; rmdir $(LAZER_PREFIX) 2>/dev/null; true
