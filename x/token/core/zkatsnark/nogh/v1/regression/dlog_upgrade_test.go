/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package regression

import (
	"context"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/services/identity/idemixnym"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/keys"
	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prepared"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/prover"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/testutils"
	snarktoken "github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/token"
	"github.com/LFDT-Panurus/panurus/x/token/core/zkatsnark/nogh/v1/validator"
)

const idemixTestdataPath = "../../../../../../../token/core/zkatdlog/nogh/v1/testdata"

// TestDLogUpgrade_Lifecycle mints a ZKAT-DLOG output, upgrades it to zkatsnark
// through the issuer (burn-and-re-issue), commits the upgrade through the
// ledger translator, spends the resulting note in an ordinary transfer, and
// checks that a second upgrade of the same ZKAT-DLOG output is refused.
func TestDLogUpgrade_Lifecycle(t *testing.T) {
	configurations, err := testutils.NewSetupConfigurationsWithParams(testutils.SetupParams{
		IdemixTestdataPath: idemixTestdataPath,
		CurveIDs:           []ecc.ID{ecc.BLS12_381},
		OwnerIdentityType:  idemixnym.IdentityType,
		LegacyDLog:         true,
	})
	require.NoError(t, err)
	configuration, err := configurations.GetSetupConfiguration(ecc.BLS12_381)
	require.NoError(t, err)

	// 1. A ZKAT-DLOG output is on the ledger.
	f, err := testutils.NewDLogUpgradeFixture(configuration, []uint64{42})
	require.NoError(t, err)
	kt := &keys.Translator{}
	rws := &memRWSet{state: map[string][]byte{}}
	for _, tok := range f.Tokens {
		key, err := kt.CreateOutputSNKey(tok.ID.TxId, tok.ID.Index, tok.Token)
		require.NoError(t, err)
		rws.state[key] = tok.Token
	}

	// 2. The issuer upgrades it; the request validates and commits.
	signed, err := f.Sign("upgrade-tx")
	require.NoError(t, err)
	require.NoError(t, f.Verify("upgrade-tx", signed))
	require.NoError(t, translator.New("upgrade-tx", rws, kt).Write(context.Background(), f.Action))

	// 3. The recipient spends the upgraded note like any other zkatsnark note.
	note, err := snarktoken.Deserialize(f.Metadata.Outputs[0].OutputMetadata)
	require.NoError(t, err)
	require.Equal(t, uint64(42), note.Value)
	spend, _, err := configuration.Orchestrator.BuildTransferAction(
		context.Background(),
		[]prover.SpendRequest{{Note: note}},
		[]prover.OutputRequest{{Value: 40, TokenType: note.TokenType, Recipient: f.Owners[0].ID}, {Value: 2, TokenType: note.TokenType, Recipient: f.Recipient.ID}},
		note.TokenType,
		configuration.PP,
		nil,
	)
	require.NoError(t, err)
	ser, err := f.Action.Outputs[0].Serialize()
	require.NoError(t, err)
	spend.InputIDs = []*token2.ID{{TxId: "upgrade-tx", Index: 0}}
	spend.InputTokens = [][]byte{ser}
	// the spend is signed by a stub owner signature only: verify it against
	// the same keys without the auditor requirement
	noAuditor := *configuration.PP
	require.NoError(t, noAuditor.SetAuditors(nil))
	verifier, err := prepared.NewVerifier(&noAuditor)
	require.NoError(t, err)
	val, err := validator.NewValidator(verifier, &testDeserializer{}, driver.ResourceLimits{}.WithDefaults())
	require.NoError(t, err)
	ledger := &testLedger{tokens: map[string][]byte{spend.InputIDs[0].String(): ser}}
	require.NoError(t, verifyTransfer(t, val, ledger, "spend-tx", spend))

	// 4. The same ZKAT-DLOG output cannot be upgraded twice: the translator
	// finds its ledger key deleted.
	require.Error(t, translator.New("upgrade-tx-2", rws, kt).Write(context.Background(), f.Action), "double upgrade")
}

// memRWSet is an in-memory translator.ExRWSet.
type memRWSet struct {
	state map[string][]byte
}

func (m *memRWSet) SetState(key translator.Key, value translator.Value) error {
	m.state[key] = value

	return nil
}

func (m *memRWSet) GetState(key translator.Key) ([]byte, error) { return m.state[key], nil }

func (m *memRWSet) DeleteState(key translator.Key) error {
	delete(m.state, key)

	return nil
}

func (m *memRWSet) StateMustNotExist(key translator.Key) error {
	if len(m.state[key]) != 0 {
		return errors.Errorf("state [%s] exists", key)
	}

	return nil
}

func (m *memRWSet) StateMustExist(key translator.Key, _ translator.KeyVersion) error {
	if len(m.state[key]) == 0 {
		return errors.Errorf("state [%s] does not exist", key)
	}

	return nil
}
