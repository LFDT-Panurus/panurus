/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/crypto"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/keys"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/statedelta"
)

// This is the Week-5 gate: the whole write path exercised against a REAL EVM node rather than a mock.
// It deploys the contracts with forge, then drives the driver's own submitter, finality manager and
// query surface against them, so what is proven is that the pieces built in isolation actually
// compose: the ABI encoding the node accepts, the transaction the node mines, the state the contract
// stores, and the finality the driver reads back.
//
// It is skipped unless anvil and forge are installed, so the ordinary unit-test run stays hermetic.

const (
	// anvilKey1 is anvil's first well-known development account, used here as endorser and submitter.
	anvilKey1 = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	anvilPort = "18545"
)

// startAnvil boots a local anvil node and returns its endpoint, skipping the test if anvil is absent.
func startAnvil(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("anvil"); err != nil {
		t.Skip("anvil not installed; skipping the end-to-end gate")
	}
	if _, err := exec.LookPath("forge"); err != nil {
		t.Skip("forge not installed; skipping the end-to-end gate")
	}

	cmd := exec.Command("anvil", "--port", anvilPort, "--silent")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	endpoint := "http://127.0.0.1:" + anvilPort
	waitForPort(t, "127.0.0.1:"+anvilPort)

	return endpoint
}

func waitForPort(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()

			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("anvil did not start listening on %s", address)
}

// deployContracts runs the project's own deploy script against the node and returns the TokenState
// clone address, so the test exercises the same deployment path an operator would use.
func deployContracts(t *testing.T, endpoint string, endorser client.Address, pp0 []byte) client.Address {
	t.Helper()

	// vm.startBroadcast() takes its sender from the CLI, so the key is passed as a flag rather than
	// through the environment.
	cmd := exec.Command("forge", "script", "script/Deploy.s.sol:Deploy",
		"--rpc-url", endpoint, "--broadcast", "--json",
		"--private-key", "0x"+anvilKey1)
	cmd.Dir = "contracts"
	cmd.Env = append(cmd.Environ(),
		"EVM_ENDORSERS="+endorser.Hex(),
		"EVM_THRESHOLD=1",
		"EVM_PP0=0x"+hex.EncodeToString(pp0),
		"EVM_GRAPH_HIDING=false",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "forge deploy failed: %s", out)

	address := parseDeployedClone(t, string(out))
	parsed, err := client.HexToAddress(address)
	require.NoError(t, err)

	return parsed
}

// parseDeployedClone pulls the TokenState clone address out of the deploy script's JSON output.
func parseDeployedClone(t *testing.T, out string) string {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var payload struct {
			Returns struct {
				TokenState struct {
					Value string `json:"value"`
				} `json:"tokenState"`
			} `json:"returns"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
			continue
		}
		if payload.Returns.TokenState.Value != "" {
			return payload.Returns.TokenState.Value
		}
	}
	t.Fatalf("could not find the deployed TokenState clone in the forge output:\n%s", out)

	return ""
}

// TestEndToEndAgainstAnvil is the Week-5 gate: issue a token, then spend it, both through the
// driver's real submission path, and read the results back from the chain.
func TestEndToEndAgainstAnvil(t *testing.T) {
	endpoint := startAnvil(t)

	keyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)
	key := secp256k1.PrivKeyFromBytes(keyBytes)
	signer, err := eip712.NewSignerFromBytes(keyBytes)
	require.NoError(t, err)

	pp0 := []byte("e2e-public-params")
	tokenState := deployContracts(t, endpoint, signer.Address(), pp0)
	t.Logf("deployed TokenState clone at %s", tokenState)

	evmClient, err := client.NewJSONRPCClient(endpoint, nil)
	require.NoError(t, err)

	cfg := validConfig()
	cfg.Endpoint = endpoint
	cfg.Contracts.TokenState = tokenState.Hex()
	cfg.Finality.BlockTag = client.BlockTagLatest // anvil mines instantly; there is no finalized tag
	cfg.Finality.PollInterval = 50 * time.Millisecond
	cfg.Finality.Timeout = 15 * time.Second
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate())

	submitter, err := NewSubmitter(evmClient, key, tokenState, big.NewInt(testChainID), cfg.Gas)
	require.NoError(t, err)

	n, err := NewNetwork("evm-net", cfg, evmClient, nil, submitter, nil)
	require.NoError(t, err)

	// The node must be the chain we signed for.
	_, err = n.Connect("token")
	require.NoError(t, err, "Connect must accept a reachable node on the configured chain")

	domain := eip712.Domain{ChainID: big.NewInt(testChainID), VerifyingContract: tokenState}

	// --- issue -------------------------------------------------------------------------------------

	issueAnchorID := n.ComputeTxID(&driver.TxID{Creator: []byte("issuer")})
	issueAnchor, err := keys.AnchorFromTxID(issueAnchorID)
	require.NoError(t, err)

	tokenData := []byte("alice-owns-100")
	issued := &statedelta.StateDelta{
		Anchor: issueAnchor,
		Outputs: []statedelta.OutputToken{{
			TokenID:   keys.ComputeTokenID(issueAnchor, 0),
			SNMarker:  keys.OutputSNMarker(issueAnchor, 0, tokenData),
			TokenData: tokenData,
		}},
		TokenRequestHash:    sha256Of("issue-request"),
		PublicParamsHash:    sha256Of(string(pp0)),
		PublicParamsVersion: 0,
	}
	issueEnv := &Envelope{Anchor: issueAnchorID, Delta: issued, Endorsements: endorse(t, signer, domain, issued)}

	require.NoError(t, n.Broadcast(t.Context(), issueEnv), "issue must be accepted on chain")
	require.NotEmpty(t, issueEnv.EthTxHash, "Broadcast must record the transaction hash")
	require.Equal(t, uint64(1), waitMined(t, evmClient, issueEnv.EthTxHash), "the issue must not revert")
	t.Logf("issue applied in %s", issueEnv.EthTxHash)

	// the token is queryable, and unspent
	issuedID := &token.ID{TxId: issueAnchorID, Index: 0}
	stored, err := n.QueryTokens(t.Context(), "token", []*token.ID{issuedID})
	require.NoError(t, err)
	assert.Equal(t, tokenData, stored[0], "the chain must return the token bytes the driver wrote")

	spent, err := n.AreTokensSpent(t.Context(), "token", []*token.ID{issuedID}, nil)
	require.NoError(t, err)
	assert.Equal(t, []bool{false}, spent, "a freshly issued token is unspent")

	// finality resolves the anchor as valid, with the recorded token-request hash
	status, trHash, _, err := n.GetTransactionStatus(t.Context(), "token", issueAnchorID)
	require.NoError(t, err)
	assert.Equal(t, driver.Valid, status)
	assert.Equal(t, issued.TokenRequestHash[:], trHash)

	// --- transfer ----------------------------------------------------------------------------------

	spendAnchorID := n.ComputeTxID(&driver.TxID{Creator: []byte("alice")})
	spendAnchor, err := keys.AnchorFromTxID(spendAnchorID)
	require.NoError(t, err)

	bobData := []byte("bob-owns-100")
	transferred := &statedelta.StateDelta{
		Anchor:    spendAnchor,
		SpentRefs: [][32]byte{keys.OutputSNMarker(issueAnchor, 0, tokenData)},
		Outputs: []statedelta.OutputToken{{
			TokenID:   keys.ComputeTokenID(spendAnchor, 0),
			SNMarker:  keys.OutputSNMarker(spendAnchor, 0, bobData),
			TokenData: bobData,
		}},
		TokenRequestHash:    sha256Of("transfer-request"),
		PublicParamsHash:    sha256Of(string(pp0)),
		PublicParamsVersion: 0,
	}
	transferEnv := &Envelope{
		Anchor:       spendAnchorID,
		Delta:        transferred,
		Endorsements: endorse(t, signer, domain, transferred),
	}

	require.NoError(t, n.Broadcast(t.Context(), transferEnv), "transfer must be accepted on chain")
	require.Equal(t, uint64(1), waitMined(t, evmClient, transferEnv.EthTxHash), "the transfer must not revert")
	t.Logf("transfer applied in %s", transferEnv.EthTxHash)

	// alice's token is now spent, bob's exists
	spent, err = n.AreTokensSpent(t.Context(), "token", []*token.ID{issuedID}, nil)
	require.NoError(t, err)
	assert.Equal(t, []bool{true}, spent, "the transferred input must be marked spent")

	bobsToken, err := n.QueryTokens(t.Context(), "token", []*token.ID{{TxId: spendAnchorID, Index: 0}})
	require.NoError(t, err)
	assert.Equal(t, bobData, bobsToken[0])

	// --- double spend ------------------------------------------------------------------------------

	// A fully endorsed but doubly-spending transaction must be refused by the contract, which is the
	// property the whole design rests on: endorsement authorizes, the chain still enforces.
	doubleAnchorID := n.ComputeTxID(&driver.TxID{Creator: []byte("mallory")})
	doubleAnchor, err := keys.AnchorFromTxID(doubleAnchorID)
	require.NoError(t, err)

	doubleSpend := &statedelta.StateDelta{
		Anchor:              doubleAnchor,
		SpentRefs:           [][32]byte{keys.OutputSNMarker(issueAnchor, 0, tokenData)}, // already spent
		TokenRequestHash:    sha256Of("double-spend-request"),
		PublicParamsHash:    sha256Of(string(pp0)),
		PublicParamsVersion: 0,
	}
	doubleEnv := &Envelope{
		Anchor:       doubleAnchorID,
		Delta:        doubleSpend,
		Endorsements: endorse(t, signer, domain, doubleSpend),
	}

	// The node may refuse it outright (gas estimation reverts) or mine it as a failed transaction;
	// either way it must never be applied.
	if err := n.Broadcast(t.Context(), doubleEnv); err == nil {
		assert.Equal(t, uint64(0), waitMined(t, evmClient, doubleEnv.EthTxHash),
			"a double spend must revert on chain")
	}

	status, _, _, err = n.GetTransactionStatus(t.Context(), "token", doubleAnchorID)
	require.NoError(t, err)
	assert.NotEqual(t, driver.Valid, status, "a double spend must never be recorded as valid")

	// --- recipient path (design §7.4) --------------------------------------------------------------

	// Bob only ever holds the anchor. He must be able to find the transaction that applied it, which is
	// what the fungible suite's CheckFinality does for a recipient.
	recipientHash, found, err := n.finality.TxHashByAnchor(t.Context(), issueAnchor)
	require.NoError(t, err)
	require.True(t, found, "a recipient must be able to resolve an applied anchor from the chain alone")
	assert.Equal(t, issueEnv.EthTxHash, recipientHash.Hex(),
		"the hash from the log metadata must be the transaction that applied the anchor")

	// An anchor that was never applied yields nothing, rather than an error: it may simply be pending.
	_, found, err = n.finality.TxHashByAnchor(t.Context(), anchorOf(t, n, "never-submitted"))
	require.NoError(t, err)
	assert.False(t, found)

	// --- finality listener -------------------------------------------------------------------------

	listener := &e2eListener{done: make(chan struct{})}
	require.NoError(t, n.AddFinalityListener("token", issueAnchorID, listener))
	select {
	case <-listener.done:
		assert.Equal(t, driver.Valid, listener.status, "an applied anchor must resolve as valid")
	case <-time.After(10 * time.Second):
		t.Fatal("the finality listener was never notified for an already-applied anchor")
	}
}

// waitMined polls until the transaction is mined and returns its receipt status. Broadcast returns as
// soon as the node accepts the transaction, so on-chain state must not be read until it is mined;
// this is the same asynchrony the finality manager exists to handle.
func waitMined(t *testing.T, c client.EVMClient, txHash string) uint64 {
	t.Helper()
	h, err := client.HexToHash(txHash)
	require.NoError(t, err)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		receipt, err := c.GetTransactionReceipt(t.Context(), h)
		require.NoError(t, err)
		if receipt != nil && receipt.BlockNumber != nil {
			return receipt.Status
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("transaction %s was never mined", txHash)

	return 0
}

// anchorOf returns the anchor for a creator that never submitted anything, for the negative case.
func anchorOf(t *testing.T, n *Network, creator string) [32]byte {
	t.Helper()
	id := n.ComputeTxID(&driver.TxID{Creator: []byte(creator)})
	anchor, err := keys.AnchorFromTxID(id)
	require.NoError(t, err)

	return anchor
}

// endorse signs the delta's EIP-712 digest, producing the quorum the contract verifies.
func endorse(t *testing.T, signer *eip712.Signer, domain eip712.Domain, delta *statedelta.StateDelta) [][]byte {
	t.Helper()
	sig, err := signer.Sign(eip712.Digest(domain, delta))
	require.NoError(t, err)

	return [][]byte{sig}
}

// sha256Of is the hash the delta carries for the token request and the public parameters.
func sha256Of(s string) [32]byte {
	var out [32]byte
	copy(out[:], crypto.SHA256([]byte(s)))

	return out
}

type e2eListener struct {
	done   chan struct{}
	status int
}

func (l *e2eListener) OnStatus(_ context.Context, _ string, status int, _ string, _ []byte) {
	l.status = status
	close(l.done)
}

func (l *e2eListener) OnError(context.Context, string, error) {}
