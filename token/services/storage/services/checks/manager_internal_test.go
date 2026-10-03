/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package checks

import (
	"testing"

	dbcommon "github.com/LFDT-Panurus/panurus/token/services/storage/db/common"
	"github.com/stretchr/testify/assert"
)

// criticalFindingsToLog is unexported, so this file tests it directly rather
// than through report's logging side effect, which has nothing to observe from
// outside the package.
func TestCriticalFindingsToLog(t *testing.T) {
	critical := func(n int) []dbcommon.Finding {
		findings := make([]dbcommon.Finding, n)
		for i := range findings {
			findings[i] = dbcommon.Finding{Severity: dbcommon.SeverityCritical}
		}

		return findings
	}

	t.Run("under the cap logs everything and omits nothing", func(t *testing.T) {
		toLog, omitted := criticalFindingsToLog(critical(3), 20)
		assert.Len(t, toLog, 3)
		assert.Equal(t, 0, omitted)
	})

	t.Run("over the cap logs exactly the cap and counts the rest as omitted", func(t *testing.T) {
		toLog, omitted := criticalFindingsToLog(critical(25), 20)
		assert.Len(t, toLog, 20)
		assert.Equal(t, 5, omitted)
	})

	t.Run("non-critical findings are neither logged nor counted as omitted", func(t *testing.T) {
		findings := append(critical(1), dbcommon.Finding{Severity: dbcommon.SeverityWarning}, dbcommon.Finding{Severity: dbcommon.SeverityInfo})
		toLog, omitted := criticalFindingsToLog(findings, 20)
		assert.Len(t, toLog, 1)
		assert.Equal(t, 0, omitted)
	})

	t.Run("no critical findings logs nothing", func(t *testing.T) {
		toLog, omitted := criticalFindingsToLog(nil, 20)
		assert.Empty(t, toLog)
		assert.Equal(t, 0, omitted)
	})
}
