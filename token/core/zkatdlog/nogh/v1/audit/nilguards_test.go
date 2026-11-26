/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package audit_test

import (
	"testing"

	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/audit"
	"github.com/stretchr/testify/require"
)

// TestAudit_NilInputs checks that the audit helpers return an error, not a panic, on nil inputs.
func TestAudit_NilInputs(t *testing.T) {
	c := math.Curves[math.BN254]
	pp := []*math.G1{c.GenG1, c.GenG1, c.GenG1}
	data := func(mutate func(d *audit.InspectableData)) *audit.InspectableToken {
		d := audit.InspectableData{Data: c.GenG1, TokenType: "ABC", Value: c.NewZrFromInt(1), BF: c.NewZrFromInt(2)}
		mutate(&d)

		return &audit.InspectableToken{Data: d}
	}
	_, _, auditor := setupAuditorTest(t)

	tests := []struct {
		name string
		call func() error
	}{
		{"InspectOutput nil Value", func() error {
			return audit.InspectOutput(t.Context(), nil, pp, c, data(func(d *audit.InspectableData) { d.Value = nil }), 0)
		}},
		{"InspectOutput nil BF", func() error {
			return audit.InspectOutput(t.Context(), nil, pp, c, data(func(d *audit.InspectableData) { d.BF = nil }), 0)
		}},
		{"InspectOutput nil curve", func() error {
			return audit.InspectOutput(t.Context(), nil, pp, nil, data(func(*audit.InspectableData) {}), 0)
		}},
		{"InspectOutput short pedersen params", func() error {
			return audit.InspectOutput(t.Context(), nil, pp[:2], c, data(func(*audit.InspectableData) {}), 0)
		}},
		{"InspectOutput nil pedersen param", func() error {
			return audit.InspectOutput(t.Context(), nil, []*math.G1{c.GenG1, nil, c.GenG1}, c, data(func(*audit.InspectableData) {}), 0)
		}},
		{"AuditorWrapper.InspectOutput nil Value", func() error {
			return auditor.InspectOutput(t.Context(), data(func(d *audit.InspectableData) { d.Value = nil }), 0)
		}},
		{"NewInspectableToken nil token", func() error {
			_, err := audit.NewInspectableToken(nil, nil, "ABC", nil, nil)

			return err
		}},
		{"InspectIdentity nil identity", func() error {
			return audit.InspectIdentity(t.Context(), nil, nil, 0)
		}},
		{"AuditorWrapper.InspectIdentity nil identity", func() error {
			return auditor.InspectIdentity(t.Context(), nil, nil, 0)
		}},
		{"InspectIdentity nil matcher", func() error {
			return audit.InspectIdentity(t.Context(), nil, &audit.InspectableIdentity{Identity: []byte("id"), AuditInfo: []byte("ai")}, 0)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { err = tc.call() })
			require.Error(t, err)
		})
	}
}
