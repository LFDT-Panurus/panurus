/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"crypto/hkdf"
	"crypto/sha256"

	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer/anoncreds"
)

// Attribute layout of a credential. All attributes are 32 bytes.
//
//	0: usk  random user secret, hidden at issuance
//	1: OU   SHA-256 of the organizational unit
//	2: EID  SHA-256 of the enrollment id
//	3: RH   SHA-256 of the revocation handle
//	4: role SHA-256 of the role
const (
	IdxUSK  uint32 = 0
	IdxOU   uint32 = 1
	IdxEID  uint32 = 2
	IdxRH   uint32 = 3
	IdxRole uint32 = 4
)

var (
	// idxCom are the attributes committed to by an identity: (nymSK, nymEID, nymRH).
	// Verifiers MUST use exactly these, they never take them from an identity.
	idxCom = []uint32{IdxUSK, IdxEID, IdxRH}
	// idxIssuance are the attributes revealed to the issuer at issuance.
	idxIssuance = []uint32{IdxOU, IdxEID, IdxRH, IdxRole}
)

const (
	attrDomain  = "panurus/lazer/attr/v1"
	idCtxDomain = "panurus/lazer/id/v1"
	sigDomain   = "panurus/lazer/sig/v1"
	skiDomain   = "panurus/lazer/ski/v1"
	// signerInfoKeyDomain separates the key that encrypts the signer infos at rest.
	signerInfoKeyDomain = "panurus/lazer/signerinfo/v1"
	// signerInfoADDomain separates the additional data of the encrypted opening of a signer info.
	signerInfoADDomain = "panurus/lazer/signerinfo/ad/v2"
	// auditKeyDomain separates the key that encrypts the opening of nymEID in the audit infos.
	auditKeyDomain = "panurus/lazer/auditopening/v1"
	// auditOpeningDomain separates the additional data of that encrypted opening.
	auditOpeningDomain = "panurus/lazer/auditopening/ad/v1"
	// eidProofDomain separates the proofs that nymEID commits to the enrollment id.
	eidProofDomain = "panurus/lazer/eidproof/v1"
)

// signerInfoKeyLen is the length of the AES-256-GCM key of the signer infos.
const signerInfoKeyLen = 32

// hashAttr maps a string value of the attribute idx to its 32 byte encoding.
func hashAttr(idx uint32, v string) []byte {
	h := sha256.New()
	h.Write([]byte(attrDomain))
	h.Write([]byte{byte(idx & 0xff)})
	h.Write([]byte(v))

	return h.Sum(nil)
}

// attributes returns the five attributes of a credential.
func attributes(usk []byte, ou, eid, rh, role string) [][]byte {
	return [][]byte{
		usk,
		hashAttr(IdxOU, ou),
		hashAttr(IdxEID, eid),
		hashAttr(IdxRH, rh),
		hashAttr(IdxRole, role),
	}
}

// attributesRevealedToIssuer returns attrs with the unrevealed attributes zeroed.
func attributesRevealedToIssuer(attrs [][]byte) [][]byte {
	out := make([][]byte, anoncreds.NAttrs)
	for i := range out {
		out[i] = make([]byte, anoncreds.AttrLen)
	}
	for _, i := range idxIssuance {
		copy(out[i], attrs[i])
	}

	return out
}

// zeroAttributes returns attributes where nothing is revealed (a presentation
// of an identity hides all attributes).
func zeroAttributes() [][]byte {
	out := make([][]byte, anoncreds.NAttrs)
	for i := range out {
		out[i] = make([]byte, anoncreds.AttrLen)
	}

	return out
}

// identityContext binds a presentation to the issuer.
func identityContext(ipk []byte) []byte {
	h := sha256.Sum256(ipk)

	return append([]byte(idCtxDomain), h[:]...)
}

// signatureContext binds a signature of knowledge to the signed message.
func signatureContext(msg []byte) []byte {
	return append([]byte(sigDomain), msg...)
}

// eidProofContext binds a proof that nym commits to the enrollment id attribute
// attr to the issuer, the nym and the attribute.
func eidProofContext(ipk, nym, attr []byte) []byte {
	h := sha256.Sum256(ipk)
	ctx := append([]byte(eidProofDomain), h[:]...)
	ctx = append(ctx, nym...)

	return append(ctx, attr...)
}

// secretKeyIdentifier is the identifier of the wallet secret in the keystore.
func secretKeyIdentifier(ipk, usk []byte) []byte {
	h := sha256.New()
	h.Write([]byte(skiDomain))
	h.Write(ipk)
	h.Write(usk)

	return h.Sum(nil)
}

// signerInfoKey derives the key that encrypts the signer infos of a wallet at
// rest from the wallet secret (usk and the user state), bound to the issuer.
func signerInfoKey(ipk, secret []byte) ([]byte, error) {
	salt := sha256.Sum256(ipk)

	return hkdf.Key(sha256.New, secret, salt[:], signerInfoKeyDomain, signerInfoKeyLen)
}

// auditKey derives the key that encrypts the opening of nymEID in the audit
// infos of a wallet from the wallet secret, bound to the issuer.
func auditKey(ipk, secret []byte) ([]byte, error) {
	salt := sha256.Sum256(ipk)

	return hkdf.Key(sha256.New, secret, salt[:], auditKeyDomain, signerInfoKeyLen)
}
