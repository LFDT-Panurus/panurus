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
// again in C, and the C decoders never read past the input they are given; no
// function panics or exits on malformed input. Methods called on a cleared
// (or zero) User, Issuer, Verifier or Commitment return ErrInvalid.
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
	"runtime"
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

// errCleared is returned by the methods of an object that was cleared.
var errCleared = errors.Wrap(ErrInvalid, "object was cleared")

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

// checkAttrs returns the NAttrs attributes concatenated. The copy may hold
// secret attributes: the caller must wipe it.
func checkAttrs(attrs [][]byte) ([]byte, error) {
	if len(attrs) != NAttrs {
		return nil, errors.Wrapf(ErrInvalid, "expected %d attributes", NAttrs)
	}
	flat := make([]byte, 0, NAttrs*AttrLen)
	for _, a := range attrs {
		if len(a) != AttrLen {
			wipe(flat[:cap(flat)])

			return nil, errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
		}
		flat = append(flat, a...)
	}

	return flat, nil
}

// wipe overwrites b with zeros.
func wipe(b []byte) {
	clear(b)
	// keep the stores from being optimized away
	runtime.KeepAlive(b)
}

// ParamsOK reports whether the proof system parameters match the constants of
// the scheme: dimensions, witness layout and every norm bound that anoncreds.c
// hard-codes.
func ParamsOK() bool { return C.cbdc_params_ok() == 1 }

// paramsCheck returns 0 if ParamsOK, else the number of the first failed check.
func paramsCheck() int { return int(C.cbdc_params_check()) }

// Proof systems, for proofHintOffset.
const (
	proofP1  = 1
	proofP2  = 2
	proofSok = 3
)

// proofHintOffset returns the bit offset of the hints in an encoded proof of
// the given system (see cbdc_proof_hint_offset). Tests use it to craft
// malformed proofs.
func proofHintOffset(system int) int { return int(C.cbdc_proof_hint_offset(C.int(system))) }

// p2WitnessWiped reports whether the presentation prover of u holds no
// witness. Tests use it to check that the witness is wiped after use.
func p2WitnessWiped(u *User) (bool, error) {
	if u == nil || u.c == nil {
		return false, errCleared
	}
	switch C.cbdc_test_p2_witness_wiped(u.c) {
	case 1:
		return true, nil
	case 0:
		return false, nil
	default:
		return false, errors.Wrapf(ErrInvalid, "no presentation prover")
	}
}

// eqOpeningStatementOK reports whether the statement of the equality-of-opening
// proof for c and attr zeroes the attribute column of the commitment key: without
// it the proof would accept a commitment to attr + x for any short x.
func eqOpeningStatementOK(c *Commitment, attr []byte) bool {
	if c == nil || c.c == nil || len(attr) != AttrLen {
		return false
	}

	return C.cbdc_test_eqopen_statement_ok(c.c, ptr(attr)) == 1
}

// gaussianSamples returns n samples of lazer's discrete gaussian with sigma
// 1.55*2^log2o from the stream (seed, dom), the sampler the provers use for
// their masking vectors. Tests use it to check the sampler.
func gaussianSamples(n int, log2o uint, seed [32]byte, dom uint32) ([]int64, error) {
	if n <= 0 {
		return nil, errors.Wrapf(ErrInvalid, "n <= 0")
	}
	out := make([]int64, n)
	rc := C.cbdc_test_grandom((*C.int64_t)(unsafe.Pointer(&out[0])), C.uint(n), C.uint(log2o),
		(*C.uint8_t)(unsafe.Pointer(&seed[0])), C.uint32_t(dom))
	if rc != 1 {
		return nil, errors.Wrapf(ErrInvalid, "gaussian samples")
	}

	return out, nil
}

// PubKeyOK reports whether pubkey is a well-formed Falcon-512 public key.
func PubKeyOK(pubkey []byte) bool {
	return len(pubkey) == PubKeyLen && C.cbdc_pubkey_ok(ptr(pubkey)) == 1
}

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

// NewUser returns a user for the issuer public key pubkey. It returns
// ErrInvalid if pubkey is not a well-formed Falcon-512 public key.
func NewUser(pubkey []byte) (*User, error) {
	if len(pubkey) != PubKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(pubkey) != %d", PubKeyLen)
	}
	c := (*C.cbdc_user_state_struct)(C.calloc(1, C.sizeof_cbdc_user_state_struct))
	if c == nil {
		return nil, errors.New("anoncreds: out of memory")
	}
	if C.cbdc_user_init(c, ptr(pubkey)) != 1 {
		C.free(unsafe.Pointer(c))

		return nil, errors.Wrapf(ErrInvalid, "malformed issuer public key")
	}

	return &User{c: c}, nil
}

// Clear releases the C state. The User must not be used afterwards.
func (u *User) Clear() {
	if u == nil || u.c == nil {
		return
	}
	C.cbdc_user_clear(u.c)
	C.free(unsafe.Pointer(u.c))
	u.c = nil
}

// MaskAttrs outputs the masked attributes (t,P1); idxRev are the indices of
// the attributes revealed to the issuer. It returns ErrInvalid if the
// attributes do not satisfy AttrsOK or an index is out of range; a previous
// masking of the user is kept then.
func (u *User) MaskAttrs(attrs [][]byte, idxRev []uint32) ([]byte, error) {
	if u == nil || u.c == nil {
		return nil, errCleared
	}
	flat, err := checkAttrs(attrs)
	if err != nil {
		return nil, err
	}
	defer wipe(flat)
	if C.cbdc_attrs_ok(ptr(flat)) != 1 {
		return nil, errors.Wrapf(ErrInvalid, "attributes exceed the norm bound, see AttrsOK")
	}
	out := make([]byte, C.cbdc_maxlen_masked_attrs())
	var outl C.size_t
	ir, nrev := idxptr(idxRev)
	if C.cbdc_user_maskattrs(u.c, ptr(out), &outl, ptr(flat), ir, nrev) != 1 {
		return nil, errors.Wrapf(ErrInvalid, "masking failed")
	}

	return out[:outl], nil
}

// Export returns the secret masking randomness of the user, to be combined
// with the attributes in Import. It returns ErrInvalid unless MaskAttrs or
// Import succeeded before.
func (u *User) Export() ([]byte, error) {
	if u == nil || u.c == nil {
		return nil, errCleared
	}
	out := make([]byte, C.cbdc_maxlen_userstate())
	var outl C.size_t
	if C.cbdc_user_export(u.c, ptr(out), &outl) != 1 {
		return nil, errors.Wrapf(ErrInvalid, "no masking to export, call MaskAttrs or Import first")
	}

	return out[:outl], nil
}

// Import restores the state exported by Export; attrs are the attributes that
// were masked.
func (u *User) Import(state []byte, attrs [][]byte) error {
	if u == nil || u.c == nil {
		return errCleared
	}
	flat, err := checkAttrs(attrs)
	if err != nil {
		return err
	}
	defer wipe(flat)
	rc := C.cbdc_user_import(u.c, ptr(state), C.size_t(len(state)), ptr(flat))
	if rc != 1 {
		return errors.Wrapf(ErrInvalid, "user state")
	}

	return nil
}

// UserTemplate is the imported state of a credential holder from which
// presentation-only Users are cloned. It never proves, so concurrent Clone
// calls on the same template are safe; Clear must not run concurrently with
// them. It owns C memory, release it with Clear.
type UserTemplate struct{ c *C.cbdc_user_state_struct }

// NewUserTemplate returns a template for the issuer public key pubkey holding
// the state exported by User.Export; attrs are the attributes that were
// masked. It returns ErrInvalid if pubkey or the state are malformed.
func NewUserTemplate(pubkey []byte, state []byte, attrs [][]byte) (*UserTemplate, error) {
	if len(pubkey) != PubKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(pubkey) != %d", PubKeyLen)
	}
	flat, err := checkAttrs(attrs)
	if err != nil {
		return nil, err
	}
	defer wipe(flat)
	c := (*C.cbdc_user_state_struct)(C.calloc(1, C.sizeof_cbdc_user_state_struct))
	if c == nil {
		return nil, errors.New("anoncreds: out of memory")
	}
	if C.cbdc_user_template_init(c, ptr(pubkey)) != 1 {
		C.free(unsafe.Pointer(c))

		return nil, errors.Wrapf(ErrInvalid, "malformed issuer public key")
	}
	t := &UserTemplate{c: c}
	if C.cbdc_user_import(c, ptr(state), C.size_t(len(state)), ptr(flat)) != 1 {
		t.Clear()

		return nil, errors.Wrapf(ErrInvalid, "user state")
	}

	return t, nil
}

// Clone returns a new User with the state of the template and its own
// presentation prover. The User can CredVerify and PresGen, but not
// MaskAttrs. It must be released with Clear.
func (t *UserTemplate) Clone() (*User, error) {
	if t == nil || t.c == nil {
		return nil, errCleared
	}
	c := (*C.cbdc_user_state_struct)(C.calloc(1, C.sizeof_cbdc_user_state_struct))
	if c == nil {
		return nil, errors.New("anoncreds: out of memory")
	}
	if C.cbdc_user_clone(c, t.c) != 1 {
		C.free(unsafe.Pointer(c))

		return nil, errors.Wrapf(ErrInvalid, "template")
	}

	return &User{c: c}, nil
}

// Clear releases the C state. The template must not be used afterwards.
func (t *UserTemplate) Clear() {
	if t == nil || t.c == nil {
		return
	}
	C.cbdc_user_clear(t.c)
	C.free(unsafe.Pointer(t.c))
	t.c = nil
}

// CredVerify checks the credential against the state of the user (after
// MaskAttrs or Import) without running a proof.
func (u *User) CredVerify(cred []byte) error {
	if u == nil || u.c == nil {
		return errCleared
	}
	rc := C.cbdc_user_credverify(u.c, ptr(cred), C.size_t(len(cred)))
	if rc != 1 {
		return errors.Wrapf(ErrInvalid, "credential")
	}

	return nil
}

// PresGen outputs a presentation bound to ctx. coms[i] is the commitment to
// the attribute with index idxCom[i].
func (u *User) PresGen(cred []byte, idxRev []uint32, coms []*Commitment, idxCom []uint32, ctx []byte) ([]byte, error) {
	if u == nil || u.c == nil {
		return nil, errCleared
	}
	if len(coms) != len(idxCom) || len(coms) > MaxCom {
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
			if c == nil || c.c == nil {
				return nil, errCleared
			}
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
	c := (*C.cbdc_commit_struct)(C.calloc(1, C.sizeof_cbdc_commit_struct))
	if c == nil {
		return nil, errors.New("anoncreds: out of memory")
	}
	if C.cbdc_user_commit(c, ptr(nym), &nymlen, ptr(attr)) != 1 {
		C.free(unsafe.Pointer(c))

		return nil, errors.Wrapf(ErrInvalid, "commitment failed")
	}

	return &Commitment{c: c, Nym: nym[:nymlen]}, nil
}

// CommitWithOpening rebuilds the commitment to attr from an opening returned by
// Opening.
func CommitWithOpening(attr []byte, opening []byte) (*Commitment, error) {
	if len(attr) != AttrLen {
		return nil, errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
	}
	nym := make([]byte, C.cbdc_maxlen_nym())
	var nymlen C.size_t
	c := (*C.cbdc_commit_struct)(C.calloc(1, C.sizeof_cbdc_commit_struct))
	if c == nil {
		return nil, errors.New("anoncreds: out of memory")
	}
	if C.cbdc_commit_import(c, ptr(nym), &nymlen, ptr(attr), ptr(opening), C.size_t(len(opening))) != 1 {
		C.free(unsafe.Pointer(c))

		return nil, errors.Wrapf(ErrInvalid, "opening")
	}

	return &Commitment{c: c, Nym: nym[:nymlen]}, nil
}

// Opening returns the serialized randomness of the commitment.
func (c *Commitment) Opening() ([]byte, error) {
	if c == nil || c.c == nil {
		return nil, errCleared
	}
	out := make([]byte, C.cbdc_maxlen_opening())
	var outl C.size_t
	if C.cbdc_commit_export(c.c, ptr(out), &outl) != 1 {
		return nil, errors.Wrapf(ErrInvalid, "opening")
	}

	return out[:outl], nil
}

// SokProve proves knowledge of the opening of the commitment, bound to ctx.
// attr must be the attribute the commitment was made to, else ErrInvalid is
// returned.
func (c *Commitment) SokProve(ctx []byte, attr []byte) ([]byte, error) {
	if c == nil || c.c == nil {
		return nil, errCleared
	}
	if len(attr) != AttrLen {
		return nil, errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
	}
	proof := make([]byte, C.cbdc_maxlen_sok())
	var proofl C.size_t
	if C.cbdc_user_sokprove(ptr(proof), &proofl, c.c, ptr(ctx), C.size_t(len(ctx)), ptr(attr)) != 1 {
		return nil, errors.Wrapf(ErrInvalid, "the commitment does not commit to attr")
	}

	return proof[:proofl], nil
}

// EqOpeningProve proves that the commitment commits to the public attribute
// attr, bound to ctx, without revealing its opening. attr must be the
// attribute the commitment was made to, else ErrInvalid is returned.
func (c *Commitment) EqOpeningProve(ctx []byte, attr []byte) ([]byte, error) {
	if c == nil || c.c == nil {
		return nil, errCleared
	}
	if len(attr) != AttrLen {
		return nil, errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
	}
	proof := make([]byte, C.cbdc_maxlen_sok())
	var proofl C.size_t
	if C.cbdc_user_eqopenprove(ptr(proof), &proofl, c.c, ptr(ctx), C.size_t(len(ctx)), ptr(attr)) != 1 {
		return nil, errors.Wrap(ErrInvalid, "the commitment does not commit to attr")
	}

	return proof[:proofl], nil
}

// Clear releases the C memory of the commitment.
func (c *Commitment) Clear() {
	if c == nil || c.c == nil {
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

// NewIssuer returns an issuer for the key pair (pubkey, privkey). It returns
// ErrInvalid if privkey is not a well-formed Falcon-512 private key whose
// public key is pubkey.
func NewIssuer(pubkey []byte, privkey []byte) (*Issuer, error) {
	if len(pubkey) != PubKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(pubkey) != %d", PubKeyLen)
	}
	if len(privkey) != PrivKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(privkey) != %d", PrivKeyLen)
	}
	c := (*C.cbdc_issuer_state_struct)(C.calloc(1, C.sizeof_cbdc_issuer_state_struct))
	if c == nil {
		return nil, errors.New("anoncreds: out of memory")
	}
	if C.cbdc_issuer_init(c, ptr(pubkey), ptr(privkey)) != 1 {
		C.free(unsafe.Pointer(c))

		return nil, errors.Wrapf(ErrInvalid, "malformed or mismatched issuer key pair")
	}

	return &Issuer{c: c}, nil
}

// Clear releases the C state. The Issuer must not be used afterwards.
func (s *Issuer) Clear() {
	if s == nil || s.c == nil {
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
	if s == nil || s.c == nil {
		return nil, errCleared
	}
	flat, err := checkAttrs(attrsRev)
	if err != nil {
		return nil, err
	}
	defer wipe(flat)
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

// NewVerifier returns a verifier for the issuer public key pubkey. It returns
// ErrInvalid if pubkey is not a well-formed Falcon-512 public key.
func NewVerifier(pubkey []byte) (*Verifier, error) {
	if len(pubkey) != PubKeyLen {
		return nil, errors.Wrapf(ErrInvalid, "len(pubkey) != %d", PubKeyLen)
	}
	c := (*C.cbdc_verifier_state_struct)(C.calloc(1, C.sizeof_cbdc_verifier_state_struct))
	if c == nil {
		return nil, errors.New("anoncreds: out of memory")
	}
	if C.cbdc_verifier_init(c, ptr(pubkey)) != 1 {
		C.free(unsafe.Pointer(c))

		return nil, errors.Wrapf(ErrInvalid, "malformed issuer public key")
	}

	return &Verifier{c: c}, nil
}

// Clear releases the C state. The Verifier must not be used afterwards.
func (v *Verifier) Clear() {
	if v == nil || v.c == nil {
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
	if v == nil || v.c == nil {
		return errCleared
	}
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

// EqOpeningVerify verifies a proof, made by EqOpeningProve, that nym is a
// commitment to the public attribute attr, bound to ctx.
func EqOpeningVerify(nym []byte, attr []byte, ctx []byte, proof []byte) error {
	if len(attr) != AttrLen {
		return errors.Wrapf(ErrInvalid, "attribute length != %d", AttrLen)
	}
	rc := C.cbdc_verifier_eqopenverify(ptr(nym), C.size_t(len(nym)), ptr(attr), ptr(ctx), C.size_t(len(ctx)), ptr(proof), C.size_t(len(proof)))
	if rc != 1 {
		return errors.Wrap(ErrInvalid, "proof does not verify")
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
