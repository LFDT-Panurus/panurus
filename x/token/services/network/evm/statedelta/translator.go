/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package statedelta

import (
	"bytes"
	"context"
	"sort"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
)

// Translator turns validated token actions into a StateDelta, the EVM analog of the Fabric RWSet
// translator (design §5.2). Its surface matches what the endorsement responder drives on the Fabric
// side (fsc/responder.go: Write per action, then AddPublicParamsDependency, then CommitTokenRequest),
// plus a StateDelta() finalizer.
//
// Every endorser must produce a byte-identical delta for the same request (§4.4), so the translator
// is deterministic by construction: outputs and spent refs are appended in action/counter order (the
// validator yields actions in request order), and metadata, collected from Go maps with random
// iteration order, is sorted by key at finalization.
//
// It consumes the same action interfaces as the Fabric translator (translator.IssueAction,
// TransferAction, SetupAction), so both shipped token drivers work unchanged. One deviation from the
// Fabric translator: an action of an unknown type is an error here, not a silent no-op, per the
// fail-fast working rule.
type Translator struct {
	anchor    [32]byte
	pp        []byte
	ppVersion uint64

	// counter is the output counter across all actions of the request; it advances exactly as the
	// Fabric translator's (verified translator.go:359/421): issue by len(outputs), transfer by
	// NumOutputs() including redeem slots, whose outputs are skipped but whose indexes are consumed.
	counter uint64

	spentRefs [][32]byte
	outputs   []OutputToken
	meta      []metaEntry

	isSetup         bool
	setupParameters []byte

	tokenRequestHash    [32]byte
	hasTokenRequestHash bool
	publicParamsHash    [32]byte
	hasPublicParams     bool
}

type metaEntry struct {
	key [32]byte
	val []byte
}

// NewTranslator returns a Translator for the token request identified by anchor, against the public
// parameters (raw bytes and on-chain version) the request was validated with.
func NewTranslator(anchor [32]byte, publicParams []byte, publicParamsVersion uint64) *Translator {
	return &Translator{
		anchor:    anchor,
		pp:        publicParams,
		ppVersion: publicParamsVersion,
	}
}

// Write translates one validated action into the delta. It routes on the same interfaces as the
// Fabric translator (issue, transfer, setup); anything else is an error.
func (t *Translator) Write(_ context.Context, action any) error {
	switch a := action.(type) {
	case translator.IssueAction:
		return t.writeIssue(a)
	case translator.TransferAction:
		return t.writeTransfer(a)
	case translator.SetupAction:
		return t.writeSetup(a)
	default:
		return errors.Errorf("unsupported action type %T", action)
	}
}

// AddPublicParamsDependency records the public parameters the delta depends on: the contract rejects
// the delta unless its hash and version match the on-chain ones at apply time (§3.4).
func (t *Translator) AddPublicParamsDependency() error {
	if len(t.pp) == 0 {
		return errors.New("no public parameters set")
	}
	copy(t.publicParamsHash[:], crypto.SHA256(t.pp))
	t.hasPublicParams = true

	return nil
}

// CommitTokenRequest hashes the marshalled token request (SHA-256, matching the hash the rest of the
// SDK stores and compares) and, when storeHash is set, records it in the delta for the contract to
// store under the anchor. It returns the hash either way.
func (t *Translator) CommitTokenRequest(raw []byte, storeHash bool) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty token request")
	}
	h := crypto.SHA256(raw)
	if storeHash {
		copy(t.tokenRequestHash[:], h)
		t.hasTokenRequestHash = true
	}

	return h, nil
}

// StateDelta finalizes and returns the delta: metadata sorted by key (canonical form), structural
// invariants validated. It fails if the translation is incomplete, that is if
// AddPublicParamsDependency or CommitTokenRequest(raw, true) has not run: signing a delta without its
// public-parameters or token-request binding would bypass the contract's checks.
func (t *Translator) StateDelta() (*StateDelta, error) {
	if !t.hasPublicParams {
		return nil, errors.New("incomplete translation: AddPublicParamsDependency has not run")
	}
	if !t.hasTokenRequestHash {
		return nil, errors.New("incomplete translation: CommitTokenRequest has not run")
	}

	sort.Slice(t.meta, func(i, j int) bool {
		return bytes.Compare(t.meta[i].key[:], t.meta[j].key[:]) < 0
	})
	metaKeys := make([][32]byte, len(t.meta))
	metaVals := make([][]byte, len(t.meta))
	for i, e := range t.meta {
		metaKeys[i] = e.key
		metaVals[i] = e.val
	}

	d := &StateDelta{
		Anchor:              t.anchor,
		SpentRefs:           t.spentRefs,
		Outputs:             t.outputs,
		MetadataKeys:        metaKeys,
		MetadataVals:        metaVals,
		TokenRequestHash:    t.tokenRequestHash,
		PublicParamsHash:    t.publicParamsHash,
		PublicParamsVersion: t.ppVersion,
		IsSetup:             t.isSetup,
		SetupParameters:     t.setupParameters,
	}
	if err := d.Validate(); err != nil {
		return nil, errors.Wrapf(err, "translated delta is invalid")
	}

	return d, nil
}

func (t *Translator) writeIssue(action translator.IssueAction) error {
	if err := t.requireNotSetup(); err != nil {
		return err
	}
	outputs, err := action.GetSerializedOutputs()
	if err != nil {
		return errors.Wrapf(err, "failed to get issue outputs")
	}
	for i, output := range outputs {
		t.appendOutput(t.counter+uint64(i), output, action.IsGraphHiding()) // #nosec G115 -- i is a small slice index
	}
	t.counter += uint64(len(outputs))

	if err := t.appendSpentRefs(action); err != nil {
		return err
	}
	for key, value := range action.GetMetadata() {
		t.meta = append(t.meta, metaEntry{key: keys.IssueMetadataKey(key), val: value})
	}

	return nil
}

func (t *Translator) writeTransfer(action translator.TransferAction) error {
	if err := t.requireNotSetup(); err != nil {
		return err
	}
	// Redeem outputs are skipped but their slot still consumes an output index, exactly as the
	// Fabric translator enumerates them.
	for i := range action.NumOutputs() {
		if action.IsRedeemAt(i) {
			continue
		}
		output, err := action.SerializeOutputAt(i)
		if err != nil {
			return errors.Wrapf(err, "failed to serialize transfer output at index %d", i)
		}
		t.appendOutput(t.counter+uint64(i), output, action.IsGraphHiding()) // #nosec G115 -- i is a small output index
	}
	t.counter += uint64(action.NumOutputs()) // #nosec G115 -- output counts are small

	if err := t.appendSpentRefs(action); err != nil {
		return err
	}
	for key, value := range action.GetMetadata() {
		t.meta = append(t.meta, metaEntry{key: keys.TransferMetadataKey(key), val: value})
	}

	return nil
}

func (t *Translator) writeSetup(action translator.SetupAction) error {
	if t.isSetup {
		return errors.New("duplicate setup action in one request")
	}
	if len(t.outputs) != 0 || len(t.spentRefs) != 0 || len(t.meta) != 0 {
		return errors.New("setup action cannot be mixed with issue or transfer actions")
	}
	raw, err := action.GetSetupParameters()
	if err != nil {
		return errors.Wrapf(err, "failed to get setup parameters")
	}
	if len(raw) == 0 {
		return errors.New("setup action carries empty public parameters")
	}
	t.isSetup = true
	t.setupParameters = raw

	return nil
}

// appendOutput records a newly created token at the given output index: its addressable id always,
// and its content-bound spend marker for graph-revealing drivers. Graph-hiding drivers spend by
// serial number, so their outputs carry a zero marker (per the frozen OutputToken contract), which
// no spend ever references.
func (t *Translator) appendOutput(index uint64, output []byte, graphHiding bool) {
	out := OutputToken{
		TokenID:   keys.ComputeTokenID(t.anchor, index),
		TokenData: output,
	}
	if !graphHiding {
		out.SNMarker = keys.OutputSNMarker(t.anchor, index, output)
	}
	t.outputs = append(t.outputs, out)
}

// appendSpentRefs records the action's consumed references: content-bound markers recomputed from
// the inputs' ids AND serialized bytes (graph-revealing; pairing GetInputs with GetSerializedInputs
// exactly as the Fabric translator's checkInputs/spendInputs do), or serial numbers (graph-hiding).
// Only one of the two lists is non-empty for any shipped driver.
func (t *Translator) appendSpentRefs(action translator.ActionWithInputs) error {
	for _, sn := range action.GetSerialNumbers() {
		t.spentRefs = append(t.spentRefs, keys.SpentRefForSerial([]byte(sn)))
	}

	inputs := action.GetInputs()
	serialized, err := action.GetSerializedInputs()
	if err != nil {
		return errors.Wrapf(err, "failed to get serialized inputs")
	}
	if len(serialized) != len(inputs) {
		return errors.Errorf("inputs and serialized inputs length mismatch: %d != %d", len(inputs), len(serialized))
	}
	for i, input := range inputs {
		if input == nil {
			return errors.Errorf("nil input at index %d", i)
		}
		inputAnchor, err := keys.AnchorFromTxID(input.TxId)
		if err != nil {
			return errors.Wrapf(err, "invalid input anchor at index %d", i)
		}
		t.spentRefs = append(t.spentRefs, keys.OutputSNMarker(inputAnchor, input.Index, serialized[i]))
	}

	return nil
}

func (t *Translator) requireNotSetup() error {
	if t.isSetup {
		return errors.New("setup action cannot be mixed with issue or transfer actions")
	}

	return nil
}
