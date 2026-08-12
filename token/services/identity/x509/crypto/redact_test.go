/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"fmt"
	"testing"

	"github.com/hyperledger/fabric-lib-go/bccsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPIN = "s3cr3t-pin-9999"

// PKCS11.String must redact the PIN and never emit it verbatim, whether the
// *PKCS11 is logged directly or as a field of a *BCCSP.
func TestPKCS11_String_RedactsPin(t *testing.T) {
	p := &PKCS11{Library: "/usr/lib/libpkcs11.so", Label: "tok", Pin: testPIN}

	s := p.String()
	assert.Contains(t, s, "[REDACTED]")
	assert.NotContains(t, s, testPIN)
	// Non-secret fields remain visible for debugging.
	assert.Contains(t, s, "/usr/lib/libpkcs11.so")
	assert.Contains(t, s, "tok")

	// %v / %s go through the Stringer as well.
	assert.NotContains(t, fmt.Sprintf("%s", p), testPIN) //nolint:staticcheck // Explicitly checking fmt.Sprintf
	assert.NotContains(t, fmt.Sprintf("%+v", p), testPIN)
}

// A nil *PKCS11 must format without panicking (SW-only configs have a nil PKCS11).
func TestPKCS11_String_Nil(t *testing.T) {
	var p *PKCS11
	assert.NotPanics(t, func() { _ = p.String() })
	assert.Equal(t, "<nil>", p.String())
	assert.NotPanics(t, func() { _ = fmt.Sprintf("%v", p) })
}

// Formatting a *BCCSP (as setup.go and kmp.go do) must redact the nested PKCS11 PIN,
// and must not panic when the PKCS11 field is nil.
func TestBCCSP_Format_RedactsPin(t *testing.T) {
	t.Run("WithPKCS11", func(t *testing.T) {
		cfg := &BCCSP{Default: "PKCS11", PKCS11: &PKCS11{Label: "tok", Pin: testPIN}}
		out := fmt.Sprintf("%v", cfg)
		assert.Contains(t, out, "[REDACTED]")
		assert.NotContains(t, out, testPIN)
	})
	t.Run("NilPKCS11", func(t *testing.T) {
		cfg := &BCCSP{Default: "SW", SW: &SoftwareProvider{Hash: "SHA2", Security: 256}}
		assert.NotPanics(t, func() { _ = fmt.Sprintf("%v", cfg) })
	})
	t.Run("NilBCCSP", func(t *testing.T) {
		var cfg *BCCSP
		assert.NotPanics(t, func() { _ = fmt.Sprintf("%v", cfg) })
	})
}

// getIdentityFactory must never mutate the caller's live config and must never panic,
// across the nil, SW-only, and error paths. Regression test for the pointer-aliasing
// redaction bug (Issue #2069) that overwrote the real PIN and dereferenced nil.
func TestGetIdentityFactory_NoMutationNoPanic(t *testing.T) {
	conf := &Config{CryptoConfig: &CryptoConfig{SignatureHashFamily: bccsp.SHA2}}

	t.Run("NilBccspConfig", func(t *testing.T) {
		// Default SW provider; nil bccspConfig is a supported, common path.
		assert.NotPanics(t, func() {
			_, err := getIdentityFactory(conf, nil, nil)
			require.NoError(t, err)
		})
	})

	t.Run("SWOnlyConfigLeavesPinUntouched", func(t *testing.T) {
		bccspConfig := &BCCSP{
			Default: "SW",
			SW:      &SoftwareProvider{Hash: "SHA2", Security: 256},
			// A PIN may be present even on the SW path; it must survive verbatim.
			PKCS11: &PKCS11{Label: "tok", Pin: testPIN},
		}

		_, err := getIdentityFactory(conf, bccspConfig, nil)
		require.NoError(t, err)
		assert.Equal(t, testPIN, bccspConfig.PKCS11.Pin, "the caller's live PIN must not be mutated")
	})

	t.Run("ErrorPathRedactsWithoutMutating", func(t *testing.T) {
		bccspConfig := &BCCSP{
			Default: "does-not-exist", // forces GetBCCSPFromConf to return an error
			PKCS11:  &PKCS11{Label: "tok", Pin: testPIN},
		}

		_, err := getIdentityFactory(conf, bccspConfig, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "[REDACTED]", "error must show redaction marker")
		assert.NotContains(t, err.Error(), testPIN, "error must not leak the PIN")
		assert.Equal(t, testPIN, bccspConfig.PKCS11.Pin, "the caller's live PIN must not be mutated")
	})
}
