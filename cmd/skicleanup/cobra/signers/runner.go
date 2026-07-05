/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package signers

import (
	"context"
	"fmt"
	"strings"

	"github.com/LFDT-Panurus/panurus/token/services/identity"
	identitydriver "github.com/LFDT-Panurus/panurus/token/services/identity/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/storage/services/cleanup"
)

// stats holds counters accumulated during a Run.
type stats struct {
	total      int // total signer entries processed
	notInToken int // entries whose identity was NOT found in the tokens table
	errors     int // entries that produced an error during processing
	skipped    int // entries found in the tokens table (not orphaned)
}

// Run iterates all entries in the Signers table in batches of batchSize.
// For each signer entry whose identity is NOT found in the Tokens table,
// it derives the SKIs and prints them to stdout.
func Run(ctx context.Context, stores *Stores, batchSize int) error {
	extractor := cleanup.NewSKIExtractor()
	extractor.RegisterProvider(idemix.IdentityTypeString, idemix.NewSKIProvider())
	extractor.RegisterProvider(idemixnym.IdentityTypeString, idemixnym.NewSKIProvider(stores.Identity))
	extractor.RegisterProvider(x509.IdentityTypeString, cleanup.NewNoopSKIProvider())

	var s stats
	offset := 0
	for {
		entries, err := stores.Identity.IterateSigners(ctx, offset, batchSize)
		if err != nil {
			return fmt.Errorf("iterate signers at offset %d: %w", offset, err)
		}

		for _, entry := range entries {
			s.total++
			result, err := processEntry(ctx, entry, stores, extractor)
			if err != nil {
				s.errors++
				// Log and continue — a single bad entry should not abort the whole scan.
				fmt.Printf("WARN: identity %s: %v\n", entry.IdentityHash, err)

				continue
			}
			if result.notInToken {
				s.notInToken++
			} else {
				s.skipped++
			}
		}

		if len(entries) < batchSize {
			// Last page reached.
			break
		}

		offset += batchSize
	}

	fmt.Printf("\n--- Statistics ---\n")
	fmt.Printf("  Total identities processed : %d\n", s.total)
	fmt.Printf("  Not found in tokens table  : %d\n", s.notInToken)
	fmt.Printf("  Found in tokens table      : %d\n", s.skipped)
	fmt.Printf("  Errors during processing   : %d\n", s.errors)

	return nil
}

// entryResult holds per-entry outcome flags returned by processEntry.
type entryResult struct {
	notInToken bool // identity was not found in the tokens table
}

func processEntry(
	ctx context.Context,
	entry identitydriver.SignerEntry,
	stores *Stores,
	extractor *cleanup.SKIExtractor,
) (entryResult, error) {
	typed, err := identity.UnmarshalTypedIdentity(entry.Identity)
	if err != nil {
		// UnmarshalTypedIdentity failed: the raw bytes are not a wrapped TypedIdentity.
		// Try idemixnym first, then idemix as a fallback.
		for _, candidate := range []struct {
			ownerType string
			idType    identity.Type
		}{
			{idemixnym.IdentityTypeString, idemixnym.IdentityType},
			{idemix.IdentityTypeString, idemix.IdentityType},
		} {
			t := &identity.TypedIdentity{Type: candidate.idType, Identity: entry.Identity}
			result, retryErr := runWithTyped(ctx, entry, stores, extractor, t, candidate.ownerType)
			if retryErr == nil {
				return result, nil
			}
		}
		// Both candidates failed; report the original unmarshal error.
		return entryResult{}, fmt.Errorf("unmarshal identity: %w", err)
	}

	ownerType := identity.TypeToString(typed.Type)

	return runWithTyped(ctx, entry, stores, extractor, typed, ownerType)
}

func runWithTyped(
	ctx context.Context,
	entry identitydriver.SignerEntry,
	stores *Stores,
	extractor *cleanup.SKIExtractor,
	typed *identity.TypedIdentity,
	ownerType string,
) (entryResult, error) {
	found, err := stores.Token.HasTokenForIdentity(ctx, typed.Identity, ownerType)
	if err != nil {
		return entryResult{}, fmt.Errorf("token lookup: %w", err)
	}

	if found {
		// Signer has tokens — not orphaned.
		return entryResult{notInToken: false}, nil
	}

	skis, err := extractor.GetSKIsFromIdentity(ctx, typed.Identity, ownerType)
	if err != nil {
		return entryResult{notInToken: true}, fmt.Errorf("extract SKIs: %w", err)
	}

	fmt.Printf("[%s][%s]: [%s]\n", entry.IdentityHash, ownerType, strings.Join(skis, ", "))

	return entryResult{notInToken: true}, nil
}
