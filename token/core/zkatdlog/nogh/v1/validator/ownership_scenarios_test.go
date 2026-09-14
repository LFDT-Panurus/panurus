/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// This file contains exactly two end-to-end scenarios, written to be read as
// stories rather than as unit tests. Both exercise the same underlying defect:
//
//	A token owner is described by TWO pieces of data that are never checked
//	against each other:
//
//	  * NymPublicKey - the pseudonym whose secret key authorises spending;
//	  * Proof        - the Idemix credential proof, which separately carries
//	                   its own copy of a pseudonym plus the commitments the
//	                   auditor opens to learn "who" the owner really is.
//
//	Honest software builds both from the same key, so they always agree.
//	Nothing enforces it. An attacker can therefore keep someone else's Proof
//	(which says "I am Bob") while substituting their own NymPublicKey (which
//	says "I hold the spending key").
//
// The cast:
//
//	Bob     - an honest user. The victim in both stories.
//	Alice   - the attacker. Crucially, she is NOT an outsider: she is simply
//	          somebody who has paid Bob before, which is how she legitimately
//	          came to hold Bob's identity data.
//	Charlie - an innocent third party who later pays Alice (scenario 1 only).
//
// Scenario 1 (standard Idemix owners): Alice makes Charlie's payment to her
// look, to the auditor, like a payment to Bob. Nobody loses money; the audit
// trail is corrupted.
//
// Scenario 2 (IdemixNym owners): Alice pays Bob, then later takes the payment
// back. Bob loses the money.
package validator

import (
	"context"
	"encoding/asn1"
	"testing"

	bccsp "github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	"github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/crypto/rp"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/issue"
	v1 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
	zktoken "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/transfer"
	tdriver "github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	idesr "github.com/LFDT-Panurus/panurus/token/services/identity/deserializer"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemix"
	icrypto "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto"
	ischema "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/schema"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym/nym"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/storage/db/kvs"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/proto"
	"github.com/stretchr/testify/require"
)

const (
	// The committed test credential. Its enrollment ID is the string "alice",
	// but in these stories the holder of this credential plays *Bob* - the
	// victim. Watch for "alice" appearing in auditor output below: that is the
	// enrollment ID baked into the fixture, i.e. Bob's real-world name.
	scenarioConfigDir = "../testdata/bls12_381_bbs/idemix"
	scenarioCurve     = math.BLS12_381_BBS_GURVY
)

// ---------------------------------------------------------------------------
// Helpers - setting up wallets and the production identity plumbing
// ---------------------------------------------------------------------------

// bobStandardIdemix is Bob when he uses a *standard Idemix* owner identity.
// With this style, the token's Owner field on the ledger is the whole identity:
// his pseudonym AND his credential proof, side by side.
type bobStandardIdemix struct {
	OwnerID   tdriver.Identity // exactly what gets written into token.Owner
	Proof     []byte           // Bob's credential proof - the piece Alice will steal
	Schema    string
	AuditInfo []byte // what the auditor uses to learn "this is Bob"
	Signer    tdriver.Signer
	IssuerPK  []byte
}

// newBobWithStandardIdemix creates Bob's wallet and has it mint one recipient
// identity - i.e. the thing Bob hands to anybody who wants to pay him.
func newBobWithStandardIdemix(t *testing.T) *bobStandardIdemix {
	t.Helper()

	backend, err := kvs.NewInMemory()
	require.NoError(t, err)
	config, err := icrypto.NewConfig(scenarioConfigDir)
	require.NoError(t, err)
	keyStore, err := icrypto.NewKeyStore(scenarioCurve, kvs.Keystore(backend))
	require.NoError(t, err)
	csp, err := icrypto.NewBCCSP(keyStore, scenarioCurve)
	require.NoError(t, err)
	km, err := idemix.NewKeyManager(config, bccsp.EidNymRhNym, csp)
	require.NoError(t, err)

	// Bob asks his wallet for a fresh identity to receive a payment on.
	desc, err := km.Identity(t.Context(), nil)
	require.NoError(t, err)
	signer, err := km.DeserializeSigner(t.Context(), desc.Identity)
	require.NoError(t, err)

	// Peek inside so the test can later pull out just the Proof.
	inner := &icrypto.SerializedIdemixIdentity{}
	require.NoError(t, proto.Unmarshal(desc.Identity, inner))

	// Identities travel wrapped in a small envelope saying what type they are.
	ownerID, err := identity.WrapWithType(idemix.IdentityType, desc.Identity)
	require.NoError(t, err)

	return &bobStandardIdemix{
		OwnerID:   ownerID,
		Proof:     inner.Proof,
		Schema:    inner.Schema,
		AuditInfo: desc.AuditInfo,
		Signer:    signer,
		IssuerPK:  config.Ipk,
	}
}

// bobIdemixNym is Bob when he uses an *IdemixNym* owner identity. With this
// style the ledger records only a short commitment to his enrollment ID (his
// "account number"). His pseudonym and credential proof are NOT on the ledger -
// they are handed over at spending time by whoever is doing the spending, which
// is precisely what scenario 2 abuses.
type bobIdemixNym struct {
	OwnerID  tdriver.Identity // token.Owner = the wrapped NymEID ("account number")
	Proof    []byte           // Bob's credential proof - handed to every payer
	Schema   string
	Signer   tdriver.Signer
	IssuerPK []byte
}

func newBobWithIdemixNym(t *testing.T) *bobIdemixNym {
	t.Helper()

	backend, err := kvs.NewInMemory()
	require.NoError(t, err)
	config, err := icrypto.NewConfig(scenarioConfigDir)
	require.NoError(t, err)
	keyStore, err := icrypto.NewKeyStore(scenarioCurve, kvs.Keystore(backend))
	require.NoError(t, err)
	csp, err := icrypto.NewBCCSP(keyStore, scenarioCurve)
	require.NoError(t, err)
	base, err := idemix.NewKeyManager(config, bccsp.EidNymRhNym, csp)
	require.NoError(t, err)

	store := kvs.NewIdentityStore(backend, tdriver.TMSID{Network: "n", Channel: "c", Namespace: "ns"})
	km := idemixnym.NewKeyManager(base, store)

	desc, err := km.Identity(t.Context(), nil)
	require.NoError(t, err)
	require.NoError(t, store.StoreSignerInfo(t.Context(), desc.Identity, desc.AuditInfo))
	signer, err := km.DeserializeSigner(t.Context(), desc.Identity)
	require.NoError(t, err)

	// IMPORTANT: with this identity style, the audit info Bob gives to a payer
	// contains his ENTIRE identity, credential proof included. So every person
	// who pays Bob is simply handed the proof Alice needs in scenario 2.
	nymAI, err := nym.DeserializeAuditInfo(desc.AuditInfo)
	require.NoError(t, err)
	inner := &icrypto.SerializedIdemixIdentity{}
	require.NoError(t, proto.Unmarshal(nymAI.IdemixSignature, inner))

	ownerID, err := identity.WrapWithType(idemixnym.IdentityType, desc.Identity)
	require.NoError(t, err)

	return &bobIdemixNym{
		OwnerID:  ownerID,
		Proof:    inner.Proof,
		Schema:   inner.Schema,
		Signer:   signer,
		IssuerPK: config.Ipk,
	}
}

// alicePseudonym is Alice's home-made pseudonym.
//
// The key thing to notice: making one involves NO issuer, NO enrollment, NO
// credential. A pseudonym is just a commitment to a random number. Alice can
// produce as many as she likes, offline, in microseconds. The system only ever
// asks her to prove she knows the secret behind it - never that anybody
// certified it.
type alicePseudonym struct {
	PublicKey []byte
	sign      func(t *testing.T, msg []byte) []byte
}

func aliceMakesPseudonym(t *testing.T, issuerPK []byte) *alicePseudonym {
	t.Helper()

	csp, err := icrypto.NewBCCSPWithDummyKeyStore(scenarioCurve)
	require.NoError(t, err)
	impOpts, err := ischema.NewDefaultManager().PublicKeyImportOpts(ischema.DefaultSchema)
	require.NoError(t, err)
	ipk, err := csp.KeyImport(issuerPK, impOpts)
	require.NoError(t, err)

	// A brand new secret key that no issuer has ever seen or signed.
	userKey, err := csp.KeyGen(&bccsp.IdemixUserSecretKeyGenOpts{Temporary: true})
	require.NoError(t, err)
	nymKey, err := csp.KeyDeriv(userKey, &bccsp.IdemixNymKeyDerivationOpts{Temporary: true, IssuerPK: ipk})
	require.NoError(t, err)
	pub, err := nymKey.PublicKey()
	require.NoError(t, err)
	pubBytes, err := pub.Bytes()
	require.NoError(t, err)

	return &alicePseudonym{
		PublicKey: pubBytes,
		sign: func(t *testing.T, msg []byte) []byte {
			t.Helper()
			sig, err := csp.Sign(userKey, msg, &bccsp.IdemixNymSignerOpts{Nym: nymKey, IssuerPK: ipk})
			require.NoError(t, err)

			return sig
		},
	}
}

// aliceForgesIdentity glues Alice's pseudonym onto Bob's credential proof.
// This single object is the whole attack: it claims to be Bob (because the
// proof is Bob's) while being controlled by Alice (because the pseudonym is
// hers).
func aliceForgesIdentity(t *testing.T, aliceNym []byte, bobProof []byte, schema string) []byte {
	t.Helper()

	raw, err := proto.Marshal(&icrypto.SerializedIdemixIdentity{
		NymPublicKey: aliceNym, // Alice's - she can sign with this
		Proof:        bobProof, // Bob's   - the auditor will read this and see Bob
		Schema:       schema,
	})
	require.NoError(t, err)

	return raw
}

// scenarioDeserializer builds the exact identity plumbing the real node uses,
// so these tests exercise production dispatch rather than a stub.
func scenarioDeserializer(t *testing.T, pp *v1.PublicParams) tdriver.Deserializer {
	t.Helper()

	des := idesr.NewTypedVerifierDeserializerMultiplex()
	for _, ipk := range pp.IdemixIssuerPublicKeys {
		base, err := idemix.NewDeserializer(ipk.PublicKey, ipk.Curve)
		require.NoError(t, err)
		des.AddTypedVerifierDeserializer(idemix.IdentityType,
			idesr.NewTypedIdentityVerifierDeserializer(base, base))
		nymDes := idemixnym.NewDeserializer(base)
		des.AddTypedVerifierDeserializer(idemixnym.IdentityType,
			idesr.NewTypedIdentityVerifierDeserializer(nymDes, nymDes))
	}

	return common.NewDeserializer(des, des, des, des, des)
}

// checkSpendIsAuthorised runs the real validator stage that decides "is this
// person allowed to move this token?", and reports whether it said yes.
func checkSpendIsAuthorised(
	t *testing.T,
	pp *v1.PublicParams,
	des tdriver.Deserializer,
	action *transfer.Action,
	message []byte,
	signature []byte,
) error {
	t.Helper()

	logger := logging.MustGetLogger()

	return TransferSignatureValidate(context.Background(), &Context{
		Logger:            logger,
		PP:                pp,
		Deserializer:      des,
		SignatureProvider: common.NewBackend(logger, nil, message, [][]byte{signature}),
		TransferAction:    action,
	})
}

// ===========================================================================
// SCENARIO 1  -  Standard Idemix owners
//
//	"Charlie pays Alice, but the auditor's books say he paid Bob."
//
// ===========================================================================
//
// The story:
//
//  1. Some time ago, Alice paid Bob. To be paid, Bob had to give Alice his
//     identity and his audit info. That is normal, required, and unavoidable -
//     Alice needed the audit info in order to forward it to the auditor.
//     Alice quietly kept a copy.
//
//  2. Today, Charlie wants to pay Alice. He asks Alice for her identity.
//
//  3. Alice sends back a forgery: her own pseudonym stapled to Bob's proof,
//     accompanied by Bob's audit info.
//
//  4. Charlie's wallet checks the identity against the audit info. This is the
//     ONLY cryptographic check a payer makes about who they are paying. It
//     passes, because it only ever inspects the proof - which really is Bob's.
//
//  5. Charlie pays. The auditor records the money as going to Bob.
//     Alice can spend it. Bob cannot, and never knew any of this happened.
//
// Nobody is robbed of money here. What is destroyed is the audit trail - the
// one thing this whole Idemix machinery exists to provide.
func TestScenario1_AliceLaundersAttributionThroughBob(t *testing.T) {
	// --- Step 1: Bob is an ordinary user who once got paid by Alice ---------
	bob := newBobWithStandardIdemix(t)

	pp, err := v1.Setup(64, bob.IssuerPK, scenarioCurve)
	require.NoError(t, err)
	des := scenarioDeserializer(t, pp)
	curve := math.Curves[pp.Curve]

	// This is what Bob handed Alice back when she paid him. Alice kept it.
	bobsProofThatAliceKept := bob.Proof
	bobsAuditInfoThatAliceKept := bob.AuditInfo

	// --- Step 2: Alice makes herself a pseudonym ---------------------------
	// No issuer involved. Nobody vouches for this. She just picks a secret.
	alice := aliceMakesPseudonym(t, bob.IssuerPK)

	// --- Step 3: Alice builds the forged identity --------------------------
	forgedRaw := aliceForgesIdentity(t, alice.PublicKey, bobsProofThatAliceKept, bob.Schema)
	forgedID, err := identity.WrapWithType(idemix.IdentityType, forgedRaw)
	require.NoError(t, err)

	// --- Step 4: Charlie checks before paying ------------------------------
	// This is literally what AnonymousOwnerWallet.RegisterRecipient does when
	// Charlie's wallet receives Alice's reply. If this rejected the forgery,
	// the attack would stop here.
	err = des.MatchIdentity(t.Context(), forgedID, bobsAuditInfoThatAliceKept)
	if err != nil {
		t.Logf("Charlie's wallet rejected Alice's forged identity: %v", err)
		t.Log("=> the attack fails at the payer's check; nothing further to test")

		return
	}
	t.Log("STEP 4: Charlie's wallet ACCEPTED the forged identity as a valid recipient")

	// What does the auditor think it is looking at? The enrollment ID inside
	// the audit info - which is Bob's ("alice" is the fixture's name for him).
	inspected, err := icrypto.DeserializeAuditInfo(bobsAuditInfoThatAliceKept)
	require.NoError(t, err)
	t.Logf("STEP 5: the auditor will record this payment as going to enrollment ID [%s]",
		inspected.EnrollmentID())

	// --- Step 5: Charlie's payment lands on a token owned by the forgery ----
	paymentToAlice := &transfer.Action{
		Inputs: []*transfer.ActionInput{{
			ID:    &token2.ID{TxId: "charlies-payment", Index: 0},
			Token: &zktoken.Token{Owner: forgedID, Data: curve.GenG1.Copy()},
		}},
		Outputs: []*zktoken.Token{{Owner: forgedID, Data: curve.GenG1.Copy()}},
	}
	message := []byte("a later transaction spending Charlie's payment")

	// --- Control: can Bob touch this money? --------------------------------
	// He should not be able to - it is not really his, despite what the books
	// say. If Bob COULD spend it, our story would be wrong.
	bobSig, err := bob.Signer.Sign(message)
	require.NoError(t, err)
	require.Error(t,
		checkSpendIsAuthorised(t, pp, des, paymentToAlice, message, bobSig),
		"control: Bob must NOT be able to spend a token that is only nominally his")
	t.Log("CONTROL: Bob cannot spend the money the auditor believes is his - as expected")

	// --- Step 6: can Alice spend it? ---------------------------------------
	aliceSig := alice.sign(t, message)
	if err := checkSpendIsAuthorised(t, pp, des, paymentToAlice, message, aliceSig); err != nil {
		t.Logf("Alice cannot spend it either: %v", err)
		t.Log("=> the funds are frozen rather than redirected")

		return
	}

	t.Error("SCENARIO 1 CONFIRMED: Charlie paid Alice; Alice can spend the money; " +
		"Bob cannot; and the auditor's records say the money went to Bob")
}

// ===========================================================================
// SCENARIO 2  -  IdemixNym owners
//
//	"Alice pays Bob, then quietly takes the payment back."
//
// ===========================================================================
//
// The story:
//
//  1. Bob publishes an account number (a commitment to his enrollment ID).
//     Anyone can pay it. Unlike scenario 1, his pseudonym and credential proof
//     are NOT stored on the ledger - they are presented at spending time.
//
//  2. Alice pays Bob 100. Two things happen as a matter of routine:
//     - Bob's audit info, which Alice needs to forward to the auditor,
//     contains Bob's full identity including his credential proof;
//     - Alice, as the sender, is the one who chooses the token's secret
//     opening (its value and blinding factor), so she knows it.
//     Neither of these is a mistake by Bob. Both are how the protocol works.
//
//  3. Later, Alice decides she wants her money back. She builds a completely
//     ordinary transfer that spends Bob's token and pays herself, proving the
//     amounts add up using the opening she kept.
//
//  4. To authorise it she needs to look like the owner. She presents Bob's
//     proof (so the account number matches) together with a fresh pseudonym of
//     her own (so she can sign). Nothing checks that these belong together.
//
//  5. Every stage of the validator accepts it. Bob's money is gone, and on the
//     ledger the reversal looks like a perfectly normal payment.
func TestScenario2_AliceTakesHerPaymentBackFromBob(t *testing.T) {
	// --- Step 1: Bob publishes an account number ---------------------------
	bob := newBobWithIdemixNym(t)

	pp, err := v1.NewWith(v1.SetupParams{
		DriverName:     v1.DLogNoGHDriverName,
		DriverVersion:  v1.ProtocolV1,
		BitLength:      64,
		IdemixIssuerPK: bob.IssuerPK,
		CurveID:        scenarioCurve,
		ProofType:      rp.CSPRangeProofType,
	})
	require.NoError(t, err)
	des := scenarioDeserializer(t, pp)
	curve := math.Curves[pp.Curve]

	// Handed to Alice as part of paying Bob. She keeps it.
	bobsProofThatAliceKept := bob.Proof

	// --- Step 2: Alice pays Bob 100 ----------------------------------------
	// As the sender, Alice creates the token: she picks the value and the
	// blinding factor, so she knows the secret opening. She keeps that too.
	commitments, openings, err := zktoken.GetTokensWithWitness(
		[]uint64{100}, "USD", pp.PedersenGenerators, curve)
	require.NoError(t, err)

	bobsToken := &zktoken.Token{Owner: bob.OwnerID, Data: commitments[0]}
	bobsTokenID := &token2.ID{TxId: "alice-pays-bob", Index: 0}
	openingAliceKept := openings[0]
	t.Log("STEP 2: Alice paid Bob 100. Bob's wallet now shows a token he believes is his.")

	// --- Step 3: Alice builds the reversal ---------------------------------
	// This is a genuine, well-formed transfer. The value proofs are real: she
	// can build them because she kept the opening from when she created the
	// token. She splits the 100 into 60 + 40, both payable to herself.
	sender, err := transfer.NewSender(
		nil,
		[]*zktoken.Token{bobsToken},
		[]*token2.ID{bobsTokenID},
		[]*zktoken.Metadata{openingAliceKept},
		pp,
	)
	require.NoError(t, err)
	reversal, _, err := sender.GenerateZKTransfer(
		t.Context(),
		[]uint64{60, 40},
		[][]byte{bob.OwnerID, bob.OwnerID}, // destination is irrelevant to this test
	)
	require.NoError(t, err,
		"Alice can build valid value proofs because she kept the token's opening")
	t.Log("STEP 3: Alice built a valid transfer spending Bob's token")

	// --- Step 4: Alice forges the ownership signature ----------------------
	message := []byte("Alice's reversal transaction")
	alice := aliceMakesPseudonym(t, bob.IssuerPK)

	// The forged "who am I" blob, presented at spending time: Bob's proof
	// (so the account number matches) plus Alice's pseudonym (so she can sign).
	forgedCreator := aliceForgesIdentity(t, alice.PublicKey, bobsProofThatAliceKept, bob.Schema)
	forgedSignature, err := asn1.Marshal(nym.Signature{
		Creator:   forgedCreator,
		Signature: alice.sign(t, message),
	})
	require.NoError(t, err)

	// --- Control: Bob really does own this token ---------------------------
	// Bob signs the very same transaction. If the validator accepts him, we
	// have proved the token genuinely belongs to Bob - so anything else the
	// validator also accepts is an impostor.
	bobSig, err := bob.Signer.Sign(message)
	require.NoError(t, err)
	require.NoError(t,
		checkSpendIsAuthorised(t, pp, des, reversal, message, bobSig),
		"control: Bob is the rightful owner and must be able to authorise this spend")
	t.Log("CONTROL: Bob can authorise this spend - confirming the token really is his")

	// --- Step 5: run the complete validator chain on Alice's forgery -------
	ctx := &Context{
		Logger:            logging.MustGetLogger(),
		PP:                pp,
		Deserializer:      des,
		SignatureProvider: common.NewBackend(logging.MustGetLogger(), nil, message, [][]byte{forgedSignature}),
		TransferAction:    reversal,
	}

	stages := []struct {
		name string
		what string
		fn   ValidateTransferFunc
	}{
		{"TransferActionValidate", "is the transaction well-formed?", TransferActionValidate},
		{"TransferSignatureValidate", "did the owner authorise this?", TransferSignatureValidate},
		{"TransferUpgradeWitnessValidate", "are any legacy-token upgrades valid?", TransferUpgradeWitnessValidate},
		{"TransferZKProofValidate", "do the amounts add up, with no money created?", TransferZKProofValidate},
		{"TransferHTLCValidate", "are any hash/time-lock rules respected?", TransferHTLCValidate},
		{"TransferApplicationDataValidate", "is the attached app metadata ok?", common.TransferApplicationDataValidate[
			*v1.PublicParams, *zktoken.Token, *transfer.Action, *issue.Action, tdriver.Deserializer]},
	}

	for _, stage := range stages {
		if err := stage.fn(context.Background(), ctx); err != nil {
			t.Logf("STOPPED at %s (%s): %v", stage.name, stage.what, err)
			t.Log("=> Alice cannot complete the reversal; Bob's money is safe")

			return
		}
		t.Logf("passed %-32s %s", stage.name, stage.what)
	}

	t.Error("SCENARIO 2 CONFIRMED: Alice paid Bob, then spent the money back to herself. " +
		"Every validator stage accepted it. On the ledger this is an ordinary transfer.")
}
