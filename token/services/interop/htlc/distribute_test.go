/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/stretchr/testify/require"
)

func validTerms() *htlc.Terms {
	return &htlc.Terms{
		ReclamationDeadline: time.Hour,
		Type1:               "USD",
		Amount1:             100,
		Type2:               "EUR",
		Amount2:             90,
	}
}

func TestTermsValidate(t *testing.T) {
	require.NoError(t, validTerms().Validate())
}

func TestTermsValidateZeroDeadline(t *testing.T) {
	terms := validTerms()
	terms.ReclamationDeadline = 0
	require.EqualError(t, terms.Validate(), "reclamation deadline should be larger than zero")
}

func TestTermsValidateNegativeDeadline(t *testing.T) {
	terms := validTerms()
	terms.ReclamationDeadline = -time.Hour
	require.EqualError(t, terms.Validate(), "reclamation deadline should be larger than zero")
}

func TestTermsValidateEmptyType1(t *testing.T) {
	terms := validTerms()
	terms.Type1 = ""
	require.EqualError(t, terms.Validate(), "types should be set")
}

func TestTermsValidateEmptyType2(t *testing.T) {
	terms := validTerms()
	terms.Type2 = ""
	require.EqualError(t, terms.Validate(), "types should be set")
}

func TestTermsValidateZeroAmount1(t *testing.T) {
	terms := validTerms()
	terms.Amount1 = 0
	require.EqualError(t, terms.Validate(), "amounts should be larger than zero")
}

func TestTermsValidateZeroAmount2(t *testing.T) {
	terms := validTerms()
	terms.Amount2 = 0
	require.EqualError(t, terms.Validate(), "amounts should be larger than zero")
}

func TestTermsBytesRoundtrip(t *testing.T) {
	original := validTerms()

	raw, err := original.Bytes()
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	parsed := &htlc.Terms{}
	require.NoError(t, parsed.FromBytes(raw))

	require.Equal(t, original.ReclamationDeadline, parsed.ReclamationDeadline)
	require.Equal(t, original.Type1, parsed.Type1)
	require.Equal(t, original.Amount1, parsed.Amount1)
	require.Equal(t, original.Type2, parsed.Type2)
	require.Equal(t, original.Amount2, parsed.Amount2)
}

func TestTermsFromBytesInvalidJSON(t *testing.T) {
	terms := &htlc.Terms{}
	require.Error(t, terms.FromBytes([]byte("not-json")))
}
