/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/
package uniqueness

import (
	"context"
	"testing"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"
)

// stubKVS is a KVS whose Get can be made to fail the way a real store does under a transient
// outage, and which records every Put so a test can assert the salt was left alone.
type stubKVS struct {
	stored   []byte
	hasValue bool
	getErr   error
	puts     [][]byte
}

func (s *stubKVS) Get(_ context.Context, k string, v any) error {
	if s.getErr != nil {
		return s.getErr
	}
	if !s.hasValue {
		return errors.Wrapf(ErrKeyNotFound, "key %s not found", k)
	}
	ptr, ok := v.(*[]byte)
	if !ok {
		return errors.Errorf("value must be *[]byte")
	}
	*ptr = s.stored

	return nil
}

func (s *stubKVS) Put(_ context.Context, _ string, v any) error {
	val, ok := v.([]byte)
	if !ok {
		return errors.Errorf("value must be []byte")
	}
	s.puts = append(s.puts, val)
	s.stored = val
	s.hasValue = true

	return nil
}

// TestComputeIDReadFailureDoesNotRotateSalt is the #2626 regression. A failed salt read must be
// propagated, not taken for "no salt yet": the old code probed with kvs.Exists, which reports a
// failed read as absence, so a transient outage made ComputeID generate a fresh salt and Put it
// over the live one, returning an ID with no error at all.
func TestComputeIDReadFailureDoesNotRotateSalt(t *testing.T) {
	t.Parallel()

	salt := []byte("the-salt-that-must-survive")
	for _, tc := range []struct {
		name   string
		getErr error
	}{
		{"store outage", errors.New("connection reset by peer")},
		{"cancelled context", context.Canceled},
		{"timeout", context.DeadlineExceeded},
		// A cause that merely ends in the words a KVS uses for absence is still a failure: the
		// classification is on the sentinel, so it cannot be fooled by wording.
		{"failure worded like a miss", errors.New(`relation "kvs" does not exist`)},
		{"failure mentioning key not found", errors.New("backend says key not found")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kvs := &stubKVS{stored: salt, hasValue: true, getErr: tc.getErr}

			id, err := NewService(kvs).ComputeID(t.Context(), "a-state")
			require.Error(t, err, "a failed salt read must not be reported as success")
			require.Empty(t, id)
			require.ErrorIs(t, err, tc.getErr)
			require.Empty(t, kvs.puts, "the stored salt must not be overwritten after a failed read")
			require.Equal(t, salt, kvs.stored)
		})
	}
}

// TestComputeIDBootstrapsSaltOnAuthoritativeMiss pins the other half of the classification: an
// ErrKeyNotFound really does mean "no salt yet", and the salt is sampled and stored once.
func TestComputeIDBootstrapsSaltOnAuthoritativeMiss(t *testing.T) {
	t.Parallel()
	kvs := &stubKVS{}

	id, err := NewService(kvs).ComputeID(t.Context(), "a-state")
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Len(t, kvs.puts, 1, "the salt must be stored exactly once")
	require.Len(t, kvs.puts[0], 32)
	require.NotEqual(t, make([]byte, 32), kvs.puts[0], "the salt must not be all zeroes")
}

// TestComputeIDBootstrapsSaltOnNilErrorMiss covers absence's second shape. The hashicorp vault
// KVS reports a missing id as a nil error with the destination left untouched
// (token/services/storage/db/kvs/hashicorp/vault_kvs.go), so classifying presence on "no error"
// alone leaves such a backend with no salt at all: never sampled, never stored, no error
// raised. Presence is therefore decided on the value being non-empty, which covers both shapes.
func TestComputeIDBootstrapsSaltOnNilErrorMiss(t *testing.T) {
	t.Parallel()
	kvs := &nilMissKVS{}

	id, err := NewService(kvs).ComputeID(t.Context(), "a-state")
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Len(t, kvs.puts, 1, "a nil-error miss must bootstrap the salt, not pass as a hit")
	require.Len(t, kvs.puts[0], 32)
}

// nilMissKVS reports a missing key the way the vault KVS does: nil error, destination untouched.
type nilMissKVS struct {
	stored []byte
	puts   [][]byte
}

func (n *nilMissKVS) Get(_ context.Context, _ string, v any) error {
	if n.stored == nil {
		return nil
	}
	ptr, ok := v.(*[]byte)
	if !ok {
		return errors.Errorf("value must be *[]byte")
	}
	*ptr = n.stored

	return nil
}

func (n *nilMissKVS) Put(_ context.Context, _ string, v any) error {
	val, ok := v.([]byte)
	if !ok {
		return errors.Errorf("value must be []byte")
	}
	n.puts = append(n.puts, val)
	n.stored = val

	return nil
}

// TestComputeIDReusesStoredSalt asserts a stored salt is read and never rewritten.
func TestComputeIDReusesStoredSalt(t *testing.T) {
	t.Parallel()
	kvs := &stubKVS{stored: []byte("stored-salt"), hasValue: true}
	svc := NewService(kvs)

	first, err := svc.ComputeID(t.Context(), "a-state")
	require.NoError(t, err)
	second, err := svc.ComputeID(t.Context(), "a-state")
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Empty(t, kvs.puts, "an existing salt must never be rewritten")
}

// TestComputeIDDoesNotTrustAnEmptyStoredSalt pins the same rule for a backend that hands back
// an empty value with no error: an empty salt is not a salt, so it must be bootstrapped.
func TestComputeIDDoesNotTrustAnEmptyStoredSalt(t *testing.T) {
	t.Parallel()
	kvs := &stubKVS{stored: []byte{}, hasValue: true}

	_, err := NewService(kvs).ComputeID(t.Context(), "a-state")
	require.NoError(t, err)
	require.Len(t, kvs.puts, 1, "an empty stored value must not pass as a stored salt")
	require.Len(t, kvs.puts[0], 32)
}

// TestComputeIDPutFailurePropagates covers the remaining error path: the salt is absent and
// storing the fresh one fails, so no ID may be returned — the next call would otherwise compute
// IDs under a salt that was never persisted.
func TestComputeIDPutFailurePropagates(t *testing.T) {
	t.Parallel()
	putErr := errors.New("disk full")

	id, err := NewService(&failingPutKVS{err: putErr}).ComputeID(t.Context(), "a-state")
	require.Error(t, err)
	require.Empty(t, id)
	require.ErrorIs(t, err, putErr)
}

type failingPutKVS struct{ err error }

func (f *failingPutKVS) Get(_ context.Context, k string, _ any) error {
	return errors.Wrapf(ErrKeyNotFound, "key %s not found", k)
}

func (f *failingPutKVS) Put(context.Context, string, any) error { return f.err }
