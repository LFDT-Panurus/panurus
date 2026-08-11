/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metricsdoc

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/disabled"
	promprovider "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics/prometheus"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token"
	commonmetrics "github.com/LFDT-Panurus/panurus/token/core/common/metrics"
	"github.com/LFDT-Panurus/panurus/token/services/auditor"
	"github.com/LFDT-Panurus/panurus/token/services/certifier/interactive"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idemixcache "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/cache"
	"github.com/LFDT-Panurus/panurus/token/services/identity/role"
	_ "github.com/LFDT-Panurus/panurus/token/services/logging" // registers the "panurus" metric namespace replacer
	fabricxqueue "github.com/LFDT-Panurus/panurus/token/services/network/fabricx/finality/queue"
	"github.com/LFDT-Panurus/panurus/token/services/selector/sherdlock"
	"github.com/LFDT-Panurus/panurus/token/services/ttx"
	ttxfinality "github.com/LFDT-Panurus/panurus/token/services/ttx/finality"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
)

const (
	goldenFile = "testdata/metrics.golden"
	// referenceDoc is the documentation page the golden file mirrors. Every name
	// in the golden file must appear there, and vice versa.
	referenceDoc = "../../../docs/development/metrics.md"
	// repoRoot locates the repository root relative to this package, for the
	// checks that read production wiring out of the source tree.
	repoRoot = "../../.."
	// updateEnv regenerates the golden file instead of comparing against it.
	updateEnv = "UPDATE_GOLDEN"
	// gapsHeading starts the part of the reference documentation that discusses
	// metrics the SDK does *not* export yet. Names proposed there are deliberately
	// not registered, so the coverage check stops at this heading.
	gapsHeading = "\n## Coverage gaps\n"
)

// tmsID supplies the network/channel/namespace values that the TMS-scoped
// provider binds to every metric it hands out. The values themselves never
// reach an exported metric name, only its labels.
var tmsID = token.TMSID{Network: "testnet", Channel: "testchannel", Namespace: "testns"}

// scope records how production reaches a group of metrics: directly with the
// provider from the dependency-injection container, or through the TMS-scoped
// wrapper returned by commonmetrics.NewTMSProvider. The distinction is not
// cosmetic - the wrapper adds a stack frame, and the exported name is derived
// from the package of the caller that Prometheus sees, so wrapped metrics are
// exported under the wrapper's package rather than their own.
//
// The column is not taken on trust: TestTMSScopedProviderWiringIsIntact pins the
// complete set of files that may build a TMS-scoped provider, so a group is
// tmsScoped exactly when its provider originates in one of them.
type scope int

const (
	// direct means production passes the container's provider straight in.
	direct scope = iota
	// tmsScoped means production passes commonmetrics.NewTMSProvider(tmsID, provider).
	tmsScoped
)

// wiringSite is a production call site of a group's constructor. Both fields are
// checked by TestWiringSitesArePresent, so a stale entry fails the test instead
// of sending a reader to the wrong file.
type wiringSite struct {
	// file is repository-relative.
	file string
	// call is the constructor invocation as it appears in that file. Call sites
	// of the same constructor do not always spell it the same way: some reach the
	// unexported constructor from inside its own package.
	call string
}

// group is one set of metrics created by a single constructor, mirroring one
// wiring site in the SDK.
type group struct {
	// subsystem is the human-readable label used in the documentation.
	subsystem string
	// source is the file that declares the metric options.
	source string
	// scope is the provider flavour production uses for this constructor.
	scope scope
	// wiring lists the production call sites of the constructor. It records where
	// the metrics are built; which provider flavour reaches them is established by
	// TestTMSScopedProviderWiringIsIntact, not by this field.
	wiring []wiringSite
	// build invokes the constructor exactly as production does.
	build func(metrics.Provider)
}

// tokenDrivers are the two files that turn the container's metrics provider into
// a TMS-scoped one. Every tmsScoped group is reached from here.
var tokenDrivers = []string{
	"token/core/fabtoken/v1/driver/driver.go",
	"token/core/zkatdlog/nogh/v1/driver/driver.go",
}

// groups enumerates every metrics constructor in the SDK. A new constructor must
// be added here, otherwise its metrics are absent from the golden file and from
// the reference documentation.
var groups = []group{
	{
		subsystem: "Driver: issue service",
		source:    "token/core/common/metrics/issue.go",
		scope:     tmsScoped,
		wiring: []wiringSite{
			{file: "token/core/fabtoken/v1/driver/driver.go", call: "metrics.NewIssueService("},
			{file: "token/core/zkatdlog/nogh/v1/driver/driver.go", call: "metrics.NewIssueService("},
		},
		build: func(p metrics.Provider) { commonmetrics.NewIssueService(nil, p) },
	},
	{
		subsystem: "Driver: transfer service",
		source:    "token/core/common/metrics/transfer.go",
		scope:     tmsScoped,
		wiring: []wiringSite{
			{file: "token/core/fabtoken/v1/driver/driver.go", call: "metrics.NewTransferService("},
			{file: "token/core/zkatdlog/nogh/v1/driver/driver.go", call: "metrics.NewTransferService("},
		},
		build: func(p metrics.Provider) { commonmetrics.NewTransferService(nil, p) },
	},
	{
		subsystem: "Driver: auditor service",
		source:    "token/core/common/metrics/auditor.go",
		scope:     tmsScoped,
		wiring: []wiringSite{
			{file: "token/core/fabtoken/v1/driver/driver.go", call: "metrics.NewAuditorService("},
			{file: "token/core/zkatdlog/nogh/v1/driver/driver.go", call: "metrics.NewAuditorService("},
		},
		build: func(p metrics.Provider) { commonmetrics.NewAuditorService(nil, p) },
	},
	{
		subsystem: "Driver: tokens service",
		source:    "token/core/common/metrics/tokens.go",
		scope:     tmsScoped,
		wiring: []wiringSite{
			{file: "token/core/fabtoken/v1/driver/driver.go", call: "metrics.NewTokensService("},
			{file: "token/core/zkatdlog/nogh/v1/driver/driver.go", call: "metrics.NewTokensService("},
		},
		build: func(p metrics.Provider) { commonmetrics.NewTokensService(nil, p) },
	},
	{
		subsystem: "Driver: tokens upgrade service",
		source:    "token/core/common/metrics/upgrade.go",
		scope:     tmsScoped,
		wiring: []wiringSite{
			{file: "token/core/fabtoken/v1/driver/driver.go", call: "metrics.NewTokensUpgradeService("},
			{file: "token/core/zkatdlog/nogh/v1/driver/driver.go", call: "metrics.NewTokensUpgradeService("},
		},
		build: func(p metrics.Provider) { commonmetrics.NewTokensUpgradeService(nil, p) },
	},
	{
		subsystem: "Identity: signer resolution",
		source:    "token/services/identity/metrics.go",
		scope:     tmsScoped,
		wiring: []wiringSite{
			{file: "token/core/fabtoken/v1/driver/ws.go", call: "identity.NewMetrics(metricsProvider)"},
			{file: "token/core/zkatdlog/nogh/v1/driver/ws.go", call: "identity.NewMetrics(metricsProvider)"},
		},
		build: func(p metrics.Provider) { identity.NewMetrics(p) },
	},
	{
		subsystem: "Identity: idemix identity cache",
		source:    "token/services/identity/idemix/cache/metrics.go",
		scope:     tmsScoped,
		wiring: []wiringSite{
			{file: "token/services/identity/idemix/kmp.go", call: "cache.NewMetrics(l.metricsProvider)"},
		},
		build: func(p metrics.Provider) { idemixcache.NewMetrics(p) },
	},
	{
		subsystem: "Identity: wallet recipient data cache",
		source:    "token/services/identity/role/metrics.go",
		scope:     tmsScoped,
		wiring: []wiringSite{
			{file: "token/services/identity/role/wallets.go", call: "NewMetrics(metricsProvider)"},
		},
		build: func(p metrics.Provider) { role.NewMetrics(p) },
	},
	{
		subsystem: "TTX: transaction lifecycle",
		source:    "token/services/ttx/metrics.go",
		scope:     direct,
		wiring: []wiringSite{
			{file: "token/sdk/dig/sdk.go", call: "p.Container().Provide(ttx.NewMetrics)"},
		},
		build: func(p metrics.Provider) { ttx.NewMetrics(p) },
	},
	{
		subsystem: "TTX: finality listener",
		source:    "token/services/ttx/finality/metrics.go",
		scope:     direct,
		wiring: []wiringSite{
			{file: "token/services/ttx/finality/listener.go", call: "newMetrics(metricsProvider)"},
			{file: "token/services/ttx/finality/recovery.go", call: "NewMetrics(metricsProvider)"},
		},
		build: func(p metrics.Provider) { ttxfinality.NewMetrics(p) },
	},
	{
		subsystem: "TTX: envelope sessions",
		source:    "token/services/utils/json/session/metrics.go",
		scope:     direct,
		wiring: []wiringSite{
			{file: "token/sdk/dig/sdk.go", call: "p.Container().Provide(jsession.NewEnvelopeMetrics)"},
		},
		build: func(p metrics.Provider) { jsession.NewEnvelopeMetrics(p) },
	},
	{
		subsystem: "Auditor service",
		source:    "token/services/auditor/metrics.go",
		scope:     direct,
		wiring: []wiringSite{
			{file: "token/services/auditor/auditor.go", call: "newMetrics(metricsProvider)"},
			{file: "token/services/auditor/manager.go", call: "newMetrics(metricsProvider)"},
		},
		build: func(p metrics.Provider) { auditor.NewMetrics(p) },
	},
	{
		subsystem: "Token selection (sherdlock)",
		source:    "token/services/selector/sherdlock/metrics.go",
		scope:     direct,
		wiring: []wiringSite{
			{file: "token/services/selector/sherdlock/service.go", call: "NewMetrics(metricsProvider)"},
			{file: "token/services/selector/sherdlock/fetcher.go", call: "NewMetrics(metricsProvider)"},
		},
		build: func(p metrics.Provider) { sherdlock.NewMetrics(p) },
	},
	{
		subsystem: "Certification service (server)",
		source:    "token/services/certifier/interactive/metrics.go",
		scope:     direct,
		wiring: []wiringSite{
			{file: "token/services/certifier/interactive/service.go", call: "NewMetrics(mp)"},
		},
		build: func(p metrics.Provider) { interactive.NewMetrics(p) },
	},
	{
		subsystem: "Certification client",
		source:    "token/services/certifier/interactive/metrics.go",
		scope:     direct,
		wiring: []wiringSite{
			{file: "token/services/certifier/interactive/client.go", call: "newClientMetrics(metricsProvider)"},
		},
		build: func(p metrics.Provider) { interactive.NewClientMetrics(p) },
	},
	{
		subsystem: "Fabric-X finality queue",
		source:    "token/services/network/fabricx/finality/queue/metrics.go",
		scope:     direct,
		wiring: []wiringSite{
			{file: "token/services/network/fabricx/finality/queue/queue.go", call: "newMetrics(cfg.MetricsProvider)"},
		},
		build: func(p metrics.Provider) { fabricxqueue.NewMetrics(p) },
	},
}

// entry is one documented metric.
type entry struct {
	// name is the fully-qualified name Prometheus exports.
	name string
	// kind is counter, gauge or histogram.
	kind string
	// labels are the declared variable labels, in declaration order.
	labels []string
	// source is the file declaring the metric options.
	source string
	// help is the metric help string.
	help string
}

func (e entry) String() string {
	labels := "-"
	if len(e.labels) > 0 {
		labels = strings.Join(e.labels, ",")
	}

	return strings.Join([]string{e.name, e.kind, labels, e.source, e.help}, " | ")
}

// TestMetricsReference collects every metric the SDK registers and checks it
// against the golden file. Run with UPDATE_GOLDEN=1 to regenerate the golden
// file after an intentional change, then update docs/development/metrics.md.
func TestMetricsReference(t *testing.T) {
	entries := collect(t)
	require.NotEmpty(t, entries)

	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, e.String())
	}
	slices.Sort(lines)
	actual := strings.Join(lines, "\n") + "\n"

	if os.Getenv(updateEnv) != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenFile), 0o750))
		require.NoError(t, os.WriteFile(goldenFile, []byte(actual), 0o644))
		t.Logf("wrote %d metrics to %s; update %s to match", len(entries), goldenFile, referenceDoc)

		return
	}

	expected, err := os.ReadFile(goldenFile)
	require.NoError(t, err, "cannot read %s; run with %s=1 to create it", goldenFile, updateEnv)
	assert.Equal(t, string(expected), actual,
		"the metrics the SDK registers no longer match %s.\n"+
			"Re-run with %s=1 to regenerate it, then update %s to match.",
		goldenFile, updateEnv, referenceDoc)
}

// TestReferenceDocumentsEveryMetric keeps docs/development/metrics.md in sync
// with the registered metrics: every metric must be documented, and the page
// must not mention names that are no longer exported.
func TestReferenceDocumentsEveryMetric(t *testing.T) {
	doc, err := os.ReadFile(referenceDoc)
	require.NoError(t, err)
	reference, _, found := strings.Cut(string(doc), gapsHeading)
	require.True(t, found, "%s no longer contains a %q section", referenceDoc, strings.TrimSpace(gapsHeading))

	registered := make(map[string]string)
	for _, e := range collect(t) {
		registered[e.name] = e.kind
	}
	documented := extractMetricNames(reference, registered)

	for name := range registered {
		assert.Contains(t, documented, name,
			"metric %q is exported but not documented in %s", name, referenceDoc)
	}
	for name := range documented {
		if _, ok := registered[name]; ok {
			continue
		}
		_, prose := prosePrefixes[name]
		assert.True(t, prose,
			"%s mentions %q, which the SDK does not register (renamed, removed or truncated?). "+
				"If it is prose about the naming scheme rather than a metric, add it to prosePrefixes.",
			referenceDoc, name)
	}
}

// TestExportedNamesCarryPackagePrefix documents, as an assertion, the rule that
// makes the exported names hard to guess: the prefix comes from the package that
// calls the provider, so metrics reached through the TMS-scoped wrapper are
// exported under that wrapper's package instead of their own.
func TestExportedNamesCarryPackagePrefix(t *testing.T) {
	for _, g := range groups {
		names := exportedNames(t, g)
		require.NotEmpty(t, names, "group %q registered no metrics", g.subsystem)

		for _, name := range names {
			assert.True(t, strings.HasPrefix(name, "panurus_"),
				"metric %q does not carry the panurus namespace; is token/services/logging imported?", name)

			if g.scope == tmsScoped {
				assert.True(t, strings.HasPrefix(name, tmsWrapperPrefix),
					"metric %q is created through the TMS-scoped provider and should be exported under %q",
					name, tmsWrapperPrefix)
			} else {
				assert.False(t, strings.HasPrefix(name, tmsWrapperPrefix),
					"metric %q is created with the plain provider and should keep its own package prefix", name)
			}
		}
	}
}

// tmsWrapperPrefix is the package prefix every TMS-scoped metric is exported
// under, because commonmetrics.tmsProvider is the caller Prometheus sees.
const tmsWrapperPrefix = "panurus_core_common_metrics_"

// tmsProviderCall is the expression that puts the TMS-scoped groups behind the
// wrapper. Passing d.metricsProvider directly instead would re-export all of
// them under their own package prefixes and invalidate most of the reference
// page - the exact failure this package exists to prevent - so the expression is
// pinned rather than inferred.
const tmsProviderCall = "metrics.NewTMSProvider(tmsConfig.ID(), d.metricsProvider)"

// tmsProviderPattern matches a call to commonmetrics.NewTMSProvider under any
// import alias ending in "metrics". It deliberately does not match the unrelated
// ftscore.NewTMSProvider in token/core/tms.go.
var tmsProviderPattern = regexp.MustCompile(`\b\w*metrics\.NewTMSProvider\(`)

// TestTMSScopedProviderWiringIsIntact pins the wiring the scope column depends
// on. The column claims that eight groups are exported under the wrapper's
// package and eight are not, and nothing in the constructors themselves says so:
// it is decided entirely by which provider production hands in. This test
// establishes it by exhaustion - the token drivers are the only files that build
// a TMS-scoped provider, and they do so from the container's provider - so a
// group reached from anywhere else cannot be TMS-scoped.
func TestTMSScopedProviderWiringIsIntact(t *testing.T) {
	found := grepRepo(t, tmsProviderPattern)
	assert.ElementsMatch(t, tokenDrivers, found,
		"the set of files building a TMS-scoped metrics provider has changed. "+
			"Every metric reached from a new call site is exported under %q instead of its own package, "+
			"so re-check the scope column and the names in %s, then update tokenDrivers.",
		tmsWrapperPrefix, referenceDoc)

	for _, driver := range tokenDrivers {
		assertFileContains(t, driver, tmsProviderCall,
			"%s no longer wraps the container's metrics provider with %s. "+
				"Without the wrapper the driver and identity metrics are exported under their own "+
				"package prefixes, and every %s* name in %s is wrong.",
			driver, tmsProviderCall, tmsWrapperPrefix, referenceDoc)
	}
}

// TestWiringSitesArePresent checks that every call site listed in a group's
// wiring still exists and still builds that group's metrics, so the pointers a
// reviewer follows stay honest.
func TestWiringSitesArePresent(t *testing.T) {
	for _, g := range groups {
		require.NotEmpty(t, g.wiring, "group %q lists no production call site", g.subsystem)

		for _, site := range g.wiring {
			assertFileContains(t, site.file, site.call,
				"group %q claims %s builds its metrics with %q, which that file no longer contains",
				g.subsystem, site.file, site.call)
		}
	}
}

// TestFoldPromQLSuffix covers the cases the reference page does not exercise
// today: a metric whose own name ends in a PromQL suffix must survive the fold,
// or the coverage check would report a correctly documented metric as missing.
func TestFoldPromQLSuffix(t *testing.T) {
	registered := map[string]string{
		"panurus_services_ttx_transaction_duration_seconds": "histogram",
		"panurus_services_ttx_retry_count":                  "counter",
		"panurus_services_ttx_accepted_transactions":        "counter",
	}

	for _, tc := range []struct {
		name     string
		in       string
		expected string
	}{
		{
			name:     "histogram bucket folds onto its family",
			in:       "panurus_services_ttx_transaction_duration_seconds_bucket",
			expected: "panurus_services_ttx_transaction_duration_seconds",
		},
		{
			name:     "histogram sum folds onto its family",
			in:       "panurus_services_ttx_transaction_duration_seconds_sum",
			expected: "panurus_services_ttx_transaction_duration_seconds",
		},
		{
			name:     "histogram count folds onto its family",
			in:       "panurus_services_ttx_transaction_duration_seconds_count",
			expected: "panurus_services_ttx_transaction_duration_seconds",
		},
		{
			name:     "a registered name ending in _count is left alone",
			in:       "panurus_services_ttx_retry_count",
			expected: "panurus_services_ttx_retry_count",
		},
		{
			name:     "a suffix is not trimmed onto a non-histogram base",
			in:       "panurus_services_ttx_accepted_transactions_count",
			expected: "panurus_services_ttx_accepted_transactions_count",
		},
		{
			name:     "an unregistered name is reported as written",
			in:       "panurus_services_ttx_accepted",
			expected: "panurus_services_ttx_accepted",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, foldPromQLSuffix(tc.in, registered))
		})
	}
}

// collect instantiates every group twice: once against a recording provider that
// captures the declared options, and once against a real Prometheus provider
// that yields the exported names. Each group gets its own provider and its own
// registry, so the two passes line up index by index within the group - a shared
// provider would deduplicate by kind and fully-qualified name and shift the
// pairing for every group after a duplicate.
func collect(t *testing.T) []entry {
	t.Helper()

	byName := make(map[string]entry)
	entries := make([]entry, 0, len(groups))
	for _, g := range groups {
		declared := declaredOptions(t, g)
		exported := exportedNames(t, g)
		require.Len(t, exported, len(declared),
			"group %q declared %v but registered %v; two metrics of the same kind sharing a "+
				"fully-qualified name are collapsed into one collector by the provider cache",
			g.subsystem, declaredNames(declared), exported)

		for i, d := range declared {
			name := exported[i]
			require.True(t, strings.HasSuffix(name, d.name),
				"exported name %q does not end in the declared name %q", name, d.name)
			e := entry{
				name:   name,
				kind:   d.kind,
				labels: d.labels,
				source: d.source,
				help:   d.help,
			}

			// Two wiring sites may legitimately ask for the same metric: the
			// provider hands both the same collector. Identical definitions are
			// therefore folded into one row, while a clash is reported by name
			// rather than as a length mismatch.
			if previous, ok := byName[e.name]; ok {
				require.Equal(t, previous, e,
					"metric %q is registered twice with different definitions; the provider would panic "+
						"on the second request", e.name)

				continue
			}
			byName[e.name] = e
			entries = append(entries, e)
		}
	}

	return entries
}

// declaration is a metric as written in the source: its bare name, kind, labels
// and help, before Prometheus prefixes it.
type declaration struct {
	kind   string
	name   string
	labels []string
	help   string
	source string
}

// declaredNames lists the bare metric names of a group, for failure messages.
func declaredNames(declared []declaration) []string {
	out := make([]string, 0, len(declared))
	for _, d := range declared {
		out = append(out, d.kind+" "+d.name)
	}

	return out
}

// declaredOptions replays one constructor against a provider that records the
// options instead of registering them.
func declaredOptions(t *testing.T, g group) []declaration {
	t.Helper()

	var out []declaration
	recorder := &recordingProvider{inner: &disabled.Provider{}, source: g.source, out: &out}
	g.build(providerFor(g, recorder))

	return out
}

// exportedNames replays one constructor against a real Prometheus provider and
// returns the fully-qualified names in creation order.
func exportedNames(t *testing.T, g group) []string {
	t.Helper()

	capture := newCapturingRegisterer(t)
	defer capture.restore()

	g.build(providerFor(g, &promprovider.Provider{}))

	return capture.names
}

// providerFor reproduces the provider flavour production hands to the group.
func providerFor(g group, base metrics.Provider) metrics.Provider {
	if g.scope == tmsScoped {
		return commonmetrics.NewTMSProvider(tmsID, base)
	}

	return base
}

// recordingProvider captures the metric options it is asked for and delegates to
// a provider that discards observations. It must not be used in the pass that
// determines exported names: delegating adds a stack frame, which would attribute
// the metrics to this package.
type recordingProvider struct {
	inner  metrics.Provider
	source string
	out    *[]declaration
}

func (p *recordingProvider) NewCounter(o metrics.CounterOpts) metrics.Counter {
	p.record("counter", o.Name, o.Help, o.LabelNames)

	return p.inner.NewCounter(o)
}

func (p *recordingProvider) NewGauge(o metrics.GaugeOpts) metrics.Gauge {
	p.record("gauge", o.Name, o.Help, o.LabelNames)

	return p.inner.NewGauge(o)
}

func (p *recordingProvider) NewHistogram(o metrics.HistogramOpts) metrics.Histogram {
	p.record("histogram", o.Name, o.Help, o.LabelNames)

	return p.inner.NewHistogram(o)
}

func (p *recordingProvider) record(kind, name, help string, labels []string) {
	*p.out = append(*p.out, declaration{
		kind:   kind,
		name:   name,
		labels: slices.Clone(labels),
		help:   help,
		source: p.source,
	})
}

// fqNamePattern extracts the fully-qualified name from a Desc. Desc keeps its
// fields private and only renders them through String().
var fqNamePattern = regexp.MustCompile(`fqName: "([^"]+)"`)

// registererMu serialises the swap of prom.DefaultRegisterer. FSC's provider
// documents that it assumes the default registerer is not swapped while it is in
// use, and the swap itself is a write to a package-level variable, so at most one
// capture may be in flight at a time - including if a test in this package is
// ever given t.Parallel().
var registererMu sync.Mutex

// capturingRegisterer stands in for the default registerer while a test runs and
// records the fully-qualified name of every collector registered through it, in
// registration order.
type capturingRegisterer struct {
	prom.Registerer
	t        *testing.T
	previous prom.Registerer
	names    []string
}

func newCapturingRegisterer(t *testing.T) *capturingRegisterer {
	t.Helper()

	registererMu.Lock()
	c := &capturingRegisterer{Registerer: prom.NewRegistry(), t: t, previous: prom.DefaultRegisterer}
	prom.DefaultRegisterer = c

	return c
}

func (c *capturingRegisterer) Register(collector prom.Collector) error {
	descs := make(chan *prom.Desc, 1)
	go func() {
		defer close(descs)
		collector.Describe(descs)
	}()

	for desc := range descs {
		match := fqNamePattern.FindStringSubmatch(desc.String())
		require.Len(c.t, match, 2, "cannot read the metric name out of %s", desc)
		c.names = append(c.names, match[1])
	}

	return c.Registerer.Register(collector)
}

// MustRegister is overridden so that name capture cannot be bypassed: the
// embedded Registerer would otherwise satisfy it directly, and a switch to
// promauto inside FSC's provider would silently stop recording names.
func (c *capturingRegisterer) MustRegister(collectors ...prom.Collector) {
	for _, collector := range collectors {
		require.NoError(c.t, c.Register(collector))
	}
}

func (c *capturingRegisterer) restore() {
	prom.DefaultRegisterer = c.previous
	registererMu.Unlock()
}

// metricNamePattern finds the SDK metric names mentioned anywhere in the
// reference documentation, including inside tables and PromQL snippets. The
// leading group keeps the match from starting inside a longer identifier, such as
// the namespace replacer key github.com_LFDT-Panurus_panurus_token.
var metricNamePattern = regexp.MustCompile(`(^|[^0-9A-Za-z_])(panurus_[a-z0-9_]+)`)

// prosePrefixes are the namespace fragments the reference page mentions while
// explaining how a name is assembled. They are not metrics, so the reverse check
// ignores them. Everything else the page mentions must be registered - notably a
// truncated name such as panurus_services_ttx_accepted, which is the likeliest
// copy-paste error in a hand-maintained table.
var prosePrefixes = map[string]struct{}{
	"panurus_services": {},
}

// extractMetricNames returns the metric names the documentation mentions. Bare
// prefixes such as "panurus_services_ttx_" are prose, not metrics, and are
// skipped. The caller is expected to pass only the part of the page that
// documents existing metrics; see gapsHeading.
func extractMetricNames(doc string, registered map[string]string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, match := range metricNamePattern.FindAllStringSubmatch(doc, -1) {
		name := match[2]
		if strings.HasSuffix(name, "_") {
			continue
		}
		out[foldPromQLSuffix(name, registered)] = struct{}{}
	}

	return out
}

// foldPromQLSuffix maps a time series a histogram exports, such as
// ..._duration_seconds_bucket, back onto the metric family it belongs to, so a
// PromQL example does not read as an undocumented metric. The fold is
// conditional: a name that is itself registered is left alone, and a suffix is
// only removed when what remains is a registered histogram. An unconditional trim
// would rewrite a counter whose own name ends in _count, _sum or _bucket and
// report it as undocumented on a page that documents it correctly.
func foldPromQLSuffix(name string, registered map[string]string) string {
	if _, ok := registered[name]; ok {
		return name
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		base, ok := strings.CutSuffix(name, suffix)
		if !ok {
			continue
		}
		if registered[base] == "histogram" {
			return base
		}
	}

	return name
}

// assertFileContains checks that a repository-relative source file still holds a
// snippet, and reports only msg on failure.
//
// It deliberately does not use assert.Contains: the haystack here is a whole
// source file, and testify prints the haystack, which buries the message under a
// few hundred lines of Go.
//
//nolint:testifylint // see above; assert.Contains would dump the entire file
func assertFileContains(t *testing.T, path, snippet, msg string, args ...any) {
	t.Helper()

	assert.True(t, strings.Contains(readRepoFile(t, path), snippet), append([]any{msg}, args...)...)
}

// readRepoFile returns the contents of a repository-relative file.
func readRepoFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
	require.NoError(t, err, "cannot read %s; has it moved?", path)

	return string(content)
}

// skippedDirs are not searched by grepRepo: they hold no first-party wiring.
var skippedDirs = map[string]struct{}{
	".git":         {},
	".github":      {},
	"docs":         {},
	"node_modules": {},
	"vendor":       {},
}

// isNestedCheckout reports whether dir is a checkout of its own - a git worktree
// or a clone - rather than part of this one. Both carry a .git entry. Such a
// directory holds a second copy of the whole repository, which would otherwise be
// walked as if it were part of this one and report every wiring file twice; the
// project's own workflow puts worktrees under .claude/worktrees.
func isNestedCheckout(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))

	return err == nil
}

// grepRepo returns the repository-relative paths of the non-test Go files that
// match pattern, sorted.
func grepRepo(t *testing.T, pattern *regexp.Regexp) []string {
	t.Helper()

	root, err := filepath.Abs(repoRoot)
	require.NoError(t, err)

	// The walk only collects candidate paths; the files are read afterwards, so
	// no filesystem operation happens inside the callback.
	var candidates []string
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, skip := skippedDirs[d.Name()]; skip {
				return fs.SkipDir
			}
			if path != root && isNestedCheckout(path) {
				return fs.SkipDir
			}

			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			candidates = append(candidates, path)
		}

		return nil
	}))

	found := make([]string, 0, len(candidates))
	for _, path := range candidates {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		if !pattern.Match(content) {
			continue
		}
		rel, err := filepath.Rel(root, path)
		require.NoError(t, err)
		found = append(found, filepath.ToSlash(rel))
	}
	slices.Sort(found)

	return found
}
