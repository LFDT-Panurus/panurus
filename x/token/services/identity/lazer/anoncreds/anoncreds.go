/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package anoncreds implements the CBDC anonymous credential scheme on top of the
// lazer library: Falcon-512 based blind issuance, selective disclosure
// presentations and attribute commitments (nyms) with signatures of knowledge.
//
// The scheme is written in C (anoncreds.c, compiled together with this package by
// cgo, with the proof system parameters in anoncreds_params.h) and links lazer.h and
// liblazer.a, which `make lazer-install` installs into <repo>/.lazer.
//
// All inputs that can come from an untrusted party are length checked here and
// again in C; no function panics on malformed input.
//
//nolint:nlreturn // cgo rewrites calls into C into code with a return statement on the same line
package anoncreds

/*
#cgo CFLAGS: -g -Wall -Wextra -I${SRCDIR}/../../../../../../.lazer/include
#cgo LDFLAGS: ${SRCDIR}/../../../../../../.lazer/lib/liblazer.a ${SRCDIR}/../../../../../../.lazer/lib/libhexl.a -lmpfr -lgmp -lm -lstdc++
#cgo darwin CFLAGS: -I/opt/homebrew/include
#cgo darwin LDFLAGS: -L/opt/homebrew/lib

#include <stdlib.h>
#include "anoncreds.h"
*/
import "C"
import (
	"unsafe"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	PubKeyLen  = 897
	PrivKeyLen = 1281
	AttrLen    = 32 // byte-length of an attribute
	NAttrs     = 5  // number of attributes
	MaxCom     = 3  // max number of committed attributes in a presentation
	HashLen    = 32 // byte-length of a message hash
)

// ErrInvalid is returned when an input is malformed or a proof does not verify.
var ErrInvalid = errors.New("anoncreds: invalid input")

// User holds the state of a credential holder. It owns C memory, release it with Clear.
type User struct{ c *C.cbdc_user_state_struct }

// Issuer holds the state of a credential issuer. It owns C memory, release it with Clear.
type Issuer struct{ c *C.cbdc_issuer_state_struct }

// Verifier holds the state for verifying presentations. It owns C memory, release it
// with Clear, and must not be used concurrently.
type Verifier struct{ c *C.cbdc_verifier_state_struct }

// Commitment is a commitment (nym) to an attribute together with its opening.
// It owns C memory and must be released with Clear.
type Commitment struct {
	c   *C.cbdc_commit_struct
	Nym []byte
}

func init() {
	C.lazer_init()
}

func ptr(b []byte) *C.uint8_t {
	if len(b) == 0 {
		return nil
	}

	return (*C.uint8_t)(unsafe.Pointer(&b[0]))
}

func idxptr(idx []uint32) (*C.uint, C.uint) {
	if len(idx) == 0 {
		return nil, 0
	}

	return (*C.uint)(unsafe.Pointer(&idx[0])), C.uint(len(idx))
}

func checkAttrs(attrs [][]byte) ([]byte, error) {
	if len(attrs) != NAttrs {
		return nil, errors.Wrapf(ErrInvalid, "expected %d attributes", NAttrs)
	}
	flat := make([]byte, 0, NAttrs*AttrLen)
	for _, a := range attrs {
		if len(a) != AttrLen {
			return nil, errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
		}
		flat = append(flat, a...)
	}

	return flat, nil
}

// ParamsOK reports whether the proof system parameters match the constants of
// the scheme (number of rows of the commitment key, maximal number of nyms).
func ParamsOK() bool { return C.cbdc_params_ok() == 1 }

// MaxLen* are the maximal lengths of the respective encodings.
func MaxLenMaskedAttrs() int { return int(C.cbdc_maxlen_masked_attrs()) }

// MaxLenCred is the maximal length of a credential.
func MaxLenCred() int { return int(C.cbdc_maxlen_cred()) }

// MaxLenPres is the maximal length of a presentation.
func MaxLenPres() int { return int(C.cbdc_maxlen_pres()) }

// MaxLenNym is the length of a nym.
func MaxLenNym() int { return int(C.cbdc_maxlen_nym()) }

// MaxLenSok is the maximal length of a signature of knowledge.
func MaxLenSok() int { return int(C.cbdc_maxlen_sok()) }

// MaxLenOpening is the length of the opening of a commitment.
func MaxLenOpening() int { return int(C.cbdc_maxlen_opening()) }

// MaxLenUserState is the length of the exported state of a user.
func MaxLenUserState() int { return int(C.cbdc_maxlen_userstate()) }

// IssuerKeygen generates a Falcon-512 keypair for the issuer.
func IssuerKeygen() (privkey []byte, pubkey []byte) {
	privkey = make([]byte, PrivKeyLen)
	pubkey = make([]byte, PubKeyLen)
	C.cbdc_issuer_keygen(ptr(privkey), ptr(pubkey))

	return privkey, pubkey
}

// AttrsOK reports whether attrs (NAttrs*AttrLen bytes, flat) satisfy the norm
// bound that the issuance proof requires. A random attribute vector fails with
// small probability; callers should resample the hidden attribute then.
func AttrsOK(attrs [][]byte) (bool, error) {
	flat, err := checkAttrs(attrs)
	if err != nil {
		return false, err
	}

	return C.cbdc_attrs_ok(ptr(flat)) == 1, nil
}

// NewUser returns a user for the issuer public key pubkey.
func NewUser(pubkey []byte) (*User, error) {
	if len(pubkey) != PubKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(pubkey) != %d", PubKeyLen)
	}
	u := &User{c: (*C.cbdc_user_state_struct)(C.calloc(1, C.sizeof_cbdc_user_state_struct))}
	C.cbdc_user_init(u.c, ptr(pubkey))

	return u, nil
}

// Clear releases the C state. The User must not be used afterwards.
func (u *User) Clear() {
	if u.c == nil {
		return
	}
	C.cbdc_user_clear(u.c)
	C.free(unsafe.Pointer(u.c))
	u.c = nil
}

// MaskAttrs outputs the masked attributes (t,P1); idxRev are the indices of
// the attributes revealed to the issuer.
func (u *User) MaskAttrs(attrs [][]byte, idxRev []uint32) ([]byte, error) {
	flat, err := checkAttrs(attrs)
	if err != nil {
		return nil, err
	}
	out := make([]byte, C.cbdc_maxlen_masked_attrs())
	var outl C.size_t
	ir, nrev := idxptr(idxRev)
	C.cbdc_user_maskattrs(u.c, ptr(out), &outl, ptr(flat), ir, nrev)

	return out[:outl], nil
}

// Export returns the secret masking randomness of the user, to be combined
// with the attributes in Import. It requires a previous call to MaskAttrs or
// Import.
func (u *User) Export() []byte {
	out := make([]byte, C.cbdc_maxlen_userstate())
	var outl C.size_t
	C.cbdc_user_export(u.c, ptr(out), &outl)

	return out[:outl]
}

// Import restores the state exported by Export; attrs are the attributes that
// were masked.
func (u *User) Import(state []byte, attrs [][]byte) error {
	flat, err := checkAttrs(attrs)
	if err != nil {
		return err
	}
	rc := C.cbdc_user_import(u.c, ptr(state), C.size_t(len(state)), ptr(flat))
	if rc != 1 {
		return errors.Wrapf(ErrInvalid, "user state")
	}

	return nil
}

// CredVerify checks the credential against the state of the user (after
// MaskAttrs or Import) without running a proof.
func (u *User) CredVerify(cred []byte) error {
	rc := C.cbdc_user_credverify(u.c, ptr(cred), C.size_t(len(cred)))
	if rc != 1 {
		return errors.Wrapf(ErrInvalid, "credential")
	}

	return nil
}

// PresGen outputs a presentation bound to ctx. coms[i] is the commitment to
// the attribute with index idxCom[i].
func (u *User) PresGen(cred []byte, idxRev []uint32, coms []*Commitment, idxCom []uint32, ctx []byte) ([]byte, error) {
	if len(coms) != len(idxCom) {
		return nil, errors.Wrapf(ErrInvalid, "len(coms) != len(idxCom)")
	}
	if len(cred) > MaxLenCred() {
		return nil, errors.Wrapf(ErrInvalid, "credential too long")
	}
	out := make([]byte, C.cbdc_maxlen_pres())
	var outl C.size_t
	ir, nrev := idxptr(idxRev)
	ic, ncom := idxptr(idxCom)

	var carr *C.cbdc_commit_ptr
	if len(coms) > 0 {
		mem := C.malloc(C.size_t(len(coms)) * C.size_t(unsafe.Sizeof(uintptr(0))))
		carr = (*C.cbdc_commit_ptr)(mem)
		defer C.free(mem)
		s := unsafe.Slice(carr, len(coms))
		for i, c := range coms {
			s[i] = c.c
		}
	}
	rc := C.cbdc_user_presgen(u.c, ptr(out), &outl, ptr(cred), C.size_t(len(cred)),
		ir, nrev, carr, ic, ncom, ptr(ctx), C.size_t(len(ctx)))
	if rc != 1 {
		return nil, errors.Wrapf(ErrInvalid, "presentation")
	}

	return out[:outl], nil
}

// Commit commits to attr using internal coins.
func Commit(attr []byte) (*Commitment, error) {
	if len(attr) != AttrLen {
		return nil, errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
	}
	nym := make([]byte, C.cbdc_maxlen_nym())
	var nymlen C.size_t
	com := &Commitment{c: (*C.cbdc_commit_struct)(C.calloc(1, C.sizeof_cbdc_commit_struct))}
	C.cbdc_user_commit(com.c, ptr(nym), &nymlen, ptr(attr))
	com.Nym = nym[:nymlen]

	return com, nil
}

// CommitWithOpening rebuilds the commitment to attr from an opening returned by
// Opening.
func CommitWithOpening(attr []byte, opening []byte) (*Commitment, error) {
	if len(attr) != AttrLen {
		return nil, errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
	}
	nym := make([]byte, C.cbdc_maxlen_nym())
	var nymlen C.size_t
	com := &Commitment{c: (*C.cbdc_commit_struct)(C.calloc(1, C.sizeof_cbdc_commit_struct))}
	if C.cbdc_commit_import(com.c, ptr(nym), &nymlen, ptr(attr), ptr(opening), C.size_t(len(opening))) != 1 {
		C.free(unsafe.Pointer(com.c))

		return nil, errors.Wrapf(ErrInvalid, "opening")
	}
	com.Nym = nym[:nymlen]

	return com, nil
}

// Opening returns the serialized randomness of the commitment.
func (c *Commitment) Opening() []byte {
	out := make([]byte, C.cbdc_maxlen_opening())
	var outl C.size_t
	C.cbdc_commit_export(c.c, ptr(out), &outl)

	return out[:outl]
}

// SokProve proves knowledge of the opening of the commitment, bound to ctx.
func (c *Commitment) SokProve(ctx []byte, attr []byte) ([]byte, error) {
	if len(attr) != AttrLen {
		return nil, errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
	}
	proof := make([]byte, C.cbdc_maxlen_sok())
	var proofl C.size_t
	C.cbdc_user_sokprove(ptr(proof), &proofl, c.c, ptr(ctx), C.size_t(len(ctx)), ptr(attr))

	return proof[:proofl], nil
}

// Clear releases the C memory of the commitment.
func (c *Commitment) Clear() {
	if c.c == nil {
		return
	}
	C.cbdc_commit_clear(c.c)
	C.free(unsafe.Pointer(c.c))
	c.c = nil
}

// OpeningVerify checks that nym is a commitment to attr with the given opening.
func OpeningVerify(nym, attr, opening []byte) bool {
	if len(attr) != AttrLen {
		return false
	}

	return C.cbdc_opening_verify(ptr(nym), C.size_t(len(nym)), ptr(attr), ptr(opening), C.size_t(len(opening))) == 1
}

// NewIssuer returns an issuer for the key pair (pubkey, privkey).
func NewIssuer(pubkey []byte, privkey []byte) (*Issuer, error) {
	if len(pubkey) != PubKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(pubkey) != %d", PubKeyLen)
	}
	if len(privkey) != PrivKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(privkey) != %d", PrivKeyLen)
	}
	s := &Issuer{c: (*C.cbdc_issuer_state_struct)(C.calloc(1, C.sizeof_cbdc_issuer_state_struct))}
	C.cbdc_issuer_init(s.c, ptr(pubkey), ptr(privkey))

	return s, nil
}

// Clear releases the C state. The Issuer must not be used afterwards.
func (s *Issuer) Clear() {
	if s.c == nil {
		return
	}
	C.cbdc_issuer_clear(s.c)
	C.free(unsafe.Pointer(s.c))
	s.c = nil
}

// Issue checks the masked attributes against the attributes revealed to the
// issuer (attrsRev, with unrevealed attributes zeroed) and outputs a
// credential.
func (s *Issuer) Issue(attrsRev [][]byte, idxRev []uint32, maskedAttrs []byte) ([]byte, error) {
	flat, err := checkAttrs(attrsRev)
	if err != nil {
		return nil, err
	}
	out := make([]byte, C.cbdc_maxlen_cred())
	var outl C.size_t
	ir, nrev := idxptr(idxRev)
	rc := C.cbdc_issuer_issue(s.c, ptr(out), &outl, ptr(flat), ir, nrev,
		ptr(maskedAttrs), C.size_t(len(maskedAttrs)))
	if rc != 1 {
		return nil, errors.Wrapf(ErrInvalid, "masked attributes")
	}

	return out[:outl], nil
}

// NewVerifier returns a verifier for the issuer public key pubkey.
func NewVerifier(pubkey []byte) (*Verifier, error) {
	if len(pubkey) != PubKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(pubkey) != %d", PubKeyLen)
	}
	v := &Verifier{c: (*C.cbdc_verifier_state_struct)(C.calloc(1, C.sizeof_cbdc_verifier_state_struct))}
	C.cbdc_verifier_init(v.c, ptr(pubkey))

	return v, nil
}

// Clear releases the C state. The Verifier must not be used afterwards.
func (v *Verifier) Clear() {
	if v.c == nil {
		return
	}
	C.cbdc_verifier_clear(v.c)
	C.free(unsafe.Pointer(v.c))
	v.c = nil
}

// PresVer verifies a presentation bound to ctx against the attributes revealed
// to the verifier (attrsRev, with unrevealed attributes zeroed) and the nyms
// (nyms[i] commits to the attribute idxCom[i]). A Verifier must not be used
// concurrently.
func (v *Verifier) PresVer(attrsRev [][]byte, idxRev []uint32, nyms [][]byte, idxCom []uint32, pres []byte, ctx []byte) error {
	flat, err := checkAttrs(attrsRev)
	if err != nil {
		return err
	}
	if len(nyms) != len(idxCom) || len(nyms) > MaxCom {
		return errors.Wrapf(ErrInvalid, "nyms")
	}
	ir, nrev := idxptr(idxRev)
	ic, ncom := idxptr(idxCom)

	var nymp **C.uint8_t
	var nymlp *C.size_t
	if len(nyms) > 0 {
		// C memory, because Go memory holding Go pointers cannot be passed to C
		pp := (*[MaxCom]*C.uint8_t)(C.malloc(C.size_t(MaxCom) * C.size_t(unsafe.Sizeof(uintptr(0)))))
		ll := (*[MaxCom]C.size_t)(C.malloc(C.size_t(MaxCom) * C.size_t(unsafe.Sizeof(C.size_t(0)))))
		defer C.free(unsafe.Pointer(pp))
		defer C.free(unsafe.Pointer(ll))
		for i, n := range nyms {
			cn := C.CBytes(n)
			defer C.free(cn)
			pp[i] = (*C.uint8_t)(cn)
			ll[i] = C.size_t(len(n))
		}
		nymp, nymlp = (**C.uint8_t)(unsafe.Pointer(pp)), (*C.size_t)(unsafe.Pointer(ll))
	}
	rc := C.cbdc_verifier_presver(v.c, ptr(flat), ir, nrev,
		(**C.uint8_t)(unsafe.Pointer(nymp)), nymlp, ic, ncom,
		ptr(pres), C.size_t(len(pres)), ptr(ctx), C.size_t(len(ctx)))
	if rc != 1 {
		return errors.Wrapf(ErrInvalid, "presentation does not verify")
	}

	return nil
}

// SokVerify verifies a proof of knowledge of the opening of nym bound to ctx.
func SokVerify(nym []byte, ctx []byte, proof []byte) error {
	rc := C.cbdc_verifier_sokverify(ptr(nym), C.size_t(len(nym)), ptr(ctx), C.size_t(len(ctx)), ptr(proof), C.size_t(len(proof)))
	if rc != 1 {
		return errors.Wrapf(ErrInvalid, "proof does not verify")
	}

	return nil
}
