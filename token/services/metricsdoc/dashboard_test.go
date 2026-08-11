/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package metricsdoc

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dashboardFile is the Grafana dashboard built from the names this package pins.
const dashboardFile = "../../../docs/monitoring/grafana/panurus.json"

// A dashboard fails differently from code: a query naming a metric that does not
// exist, or filtering on a label the metric does not carry, renders an empty
// panel rather than an error. "No data" is indistinguishable from "nothing
// happened", which is how the dashboard in #1749 shipped with every query broken.
// The tests below turn that silence into a build failure.

// dashboardSelector matches an instant-vector selector together with its label
// matcher block, e.g. panurus_services_ttx_accepted_transactions{network=~"$n"}.
var dashboardSelector = regexp.MustCompile(`(panurus_[a-z0-9_]+)\{([^}]*)\}`)

// labelMatcherName pulls the label name out of each matcher in a selector block.
var labelMatcherName = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:=~|!~|=|!=)`)

// groupByClause matches the label list of a PromQL "by (...)" grouping.
var groupByClause = regexp.MustCompile(`\bby\s*\(([^)]*)\)`)

// dashboardVariable matches a Grafana template variable reference.
var dashboardVariable = regexp.MustCompile(`\$(?:\{)?([A-Za-z_][A-Za-z0-9_]*)`)

// metricPosition matches an identifier used where PromQL expects a metric name:
// immediately before a label matcher block or a range selector. PromQL functions
// are followed by "(" and so never match.
var metricPosition = regexp.MustCompile(`(?:^|[^A-Za-z0-9_:$])([a-zA-Z_][a-zA-Z0-9_:]*)\s*[{\[]`)

// syntheticLabels are produced by Prometheus rather than declared by the SDK, so
// a query may group by them even though no metric lists them.
var syntheticLabels = []string{"le"}

// grafanaBuiltinVariables are supplied by Grafana itself and need no entry in the
// dashboard's own template variable list.
var grafanaBuiltinVariables = []string{
	"__rate_interval",
	"__interval",
	"__interval_ms",
	"__range",
	"__from",
	"__to",
	"__auto_interval",
}

// TestDashboardQueriesReferenceRegisteredMetrics checks that every metric the
// dashboard queries is one the SDK actually registers. This is the failure the
// dashboard in #1749 shipped with: each expr used the bare option name from the
// Go source, so no query matched anything in a real Prometheus.
func TestDashboardQueriesReferenceRegisteredMetrics(t *testing.T) {
	registered, _ := registeredMetrics(t)
	values := dashboardStrings(t)
	require.NotEmpty(t, values, "%s contains no strings; did it fail to parse?", dashboardFile)

	referenced := make(map[string]struct{})
	for _, s := range values {
		for _, match := range metricNamePattern.FindAllStringSubmatch(s, -1) {
			name := foldPromQLSuffix(match[2], registered)
			referenced[name] = struct{}{}
			assert.Contains(t, registered, name,
				"%s queries %q, which the SDK does not register. A Grafana panel does not error on "+
					"an unknown metric, it renders \"No data\" - check the name against %s.",
				dashboardFile, match[2], goldenFile)
		}
	}
	require.NotEmpty(t, referenced, "%s references no SDK metric at all", dashboardFile)
	t.Logf("%s references %d of the %d registered metrics", dashboardFile, len(referenced), len(registered))
}

// TestDashboardQueriesUseExportedNames catches the specific mistake #1749 shipped:
// every expr there used the bare Name from the Go source, which carries no
// package prefix and therefore matches nothing. Such a name is invisible to the
// registration check above - it does not even look like an SDK metric - so it is
// asserted separately, at the position PromQL expects a metric name.
func TestDashboardQueriesUseExportedNames(t *testing.T) {
	for _, query := range dashboardQueries(t) {
		for _, match := range metricPosition.FindAllStringSubmatch(query, -1) {
			name := match[1]
			assert.True(t, strings.HasPrefix(name, "panurus_"),
				"%s selects %q, which carries no package prefix. Prometheus exports SDK metrics "+
					"under a panurus_* name assembled at registration time, so a bare option name "+
					"from the Go source matches nothing; see %s for the exported names.",
				dashboardFile, name, goldenFile)
		}
	}
}

// TestDashboardLabelFiltersMatchDeclaredLabels checks that every label a query
// filters or groups on is declared by the metric it is applied to. Filtering on a
// label a metric does not carry silently matches nothing, so this is the second
// way a panel goes blank without anyone noticing.
func TestDashboardLabelFiltersMatchDeclaredLabels(t *testing.T) {
	registered, declaredLabels := registeredMetrics(t)

	for _, query := range dashboardQueries(t) {
		selectors := dashboardSelector.FindAllStringSubmatch(query, -1)
		for _, selector := range selectors {
			name := foldPromQLSuffix(selector[1], registered)
			labels, ok := declaredLabels[name]
			if !ok {
				continue // reported by TestDashboardQueriesReferenceRegisteredMetrics
			}
			for _, matcher := range labelMatcherName.FindAllStringSubmatch(selector[2], -1) {
				assert.Contains(t, labels, matcher[1],
					"%s filters %s on label %q, which that metric does not declare (it has %v). "+
						"The selector matches no series and the panel renders \"No data\".",
					dashboardFile, name, matcher[1], labels)
			}
		}

		// A "by (...)" clause groups the result of a subexpression, so it can only
		// be attributed to a metric when the query names exactly one. The names are
		// taken from the whole query rather than from the selectors above: a metric
		// queried with a range but no label filter has no selector block at all.
		names := distinctMetricNames(query, registered)
		if len(names) != 1 {
			continue
		}
		labels, ok := declaredLabels[names[0]]
		if !ok {
			continue
		}
		for _, clause := range groupByClause.FindAllStringSubmatch(query, -1) {
			for label := range strings.SplitSeq(clause[1], ",") {
				label = strings.TrimSpace(label)
				if label == "" || slices.Contains(syntheticLabels, label) {
					continue
				}
				assert.Contains(t, labels, label,
					"%s groups %s by label %q, which that metric does not declare (it has %v). "+
						"Grouping by an absent label collapses every series into one.",
					dashboardFile, names[0], label, labels)
			}
		}
	}
}

// TestDashboardVariablesAreDeclared checks that every template variable a query
// interpolates is defined by the dashboard, so a renamed variable cannot leave a
// panel filtering on an empty string.
func TestDashboardVariablesAreDeclared(t *testing.T) {
	declared := dashboardVariableNames(t)
	require.NotEmpty(t, declared, "%s declares no template variables", dashboardFile)

	for _, query := range dashboardQueries(t) {
		for _, match := range dashboardVariable.FindAllStringSubmatch(query, -1) {
			name := match[1]
			if slices.Contains(grafanaBuiltinVariables, name) {
				continue
			}
			assert.Contains(t, declared, name,
				"%s interpolates $%s, which is neither a Grafana built-in nor declared in the "+
					"dashboard's templating list", dashboardFile, name)
		}
	}
}

// registeredMetrics returns the exported metrics as name->kind and name->labels.
func registeredMetrics(t *testing.T) (map[string]string, map[string][]string) {
	t.Helper()

	kinds := make(map[string]string)
	labels := make(map[string][]string)
	for _, e := range collect(t) {
		kinds[e.name] = e.kind
		labels[e.name] = e.labels
	}

	return kinds, labels
}

// distinctMetricNames returns the metric families a query refers to, folded onto
// their registered names.
func distinctMetricNames(query string, registered map[string]string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, match := range metricNamePattern.FindAllStringSubmatch(query, -1) {
		name := foldPromQLSuffix(match[2], registered)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}

	return out
}

// dashboardStrings returns every string value in the dashboard, so a metric name
// is checked wherever it appears - a query, a panel title, a description.
func dashboardStrings(t *testing.T) []string {
	t.Helper()

	var out []string
	walkJSON(loadDashboard(t), func(key, value string) {
		out = append(out, value)
	})

	return out
}

// dashboardQueries returns the PromQL the dashboard runs: panel targets and the
// queries behind template variables.
func dashboardQueries(t *testing.T) []string {
	t.Helper()

	var out []string
	walkJSON(loadDashboard(t), func(key, value string) {
		if key == "expr" || key == "query" {
			out = append(out, value)
		}
	})
	require.NotEmpty(t, out, "%s defines no queries", dashboardFile)

	return out
}

// dashboardVariableNames returns the names in the dashboard's templating list.
func dashboardVariableNames(t *testing.T) []string {
	t.Helper()

	var dashboard struct {
		Templating struct {
			List []struct {
				Name string `json:"name"`
			} `json:"list"`
		} `json:"templating"`
	}
	require.NoError(t, json.Unmarshal(readDashboard(t), &dashboard))

	out := make([]string, 0, len(dashboard.Templating.List))
	for _, v := range dashboard.Templating.List {
		out = append(out, v.Name)
	}

	return out
}

func readDashboard(t *testing.T) []byte {
	t.Helper()

	content, err := os.ReadFile(dashboardFile)
	require.NoError(t, err, "cannot read %s", dashboardFile)

	return content
}

func loadDashboard(t *testing.T) any {
	t.Helper()

	var dashboard any
	require.NoError(t, json.Unmarshal(readDashboard(t), &dashboard),
		"%s is not valid JSON; Grafana would refuse the import", dashboardFile)

	return dashboard
}

// walkJSON visits every string in a decoded JSON document, passing the key it was
// found under (empty for array elements and the root).
func walkJSON(node any, visit func(key, value string)) {
	switch n := node.(type) {
	case map[string]any:
		for key, value := range n {
			if s, ok := value.(string); ok {
				visit(key, s)

				continue
			}
			walkJSON(value, visit)
		}
	case []any:
		for _, value := range n {
			if s, ok := value.(string); ok {
				visit("", s)

				continue
			}
			walkJSON(value, visit)
		}
	}
}
