/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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
	// anvilStrangerKey is anvil's second well-known account, used where a test needs a real key the
	// deployed contracts do not know.
	anvilStrangerKey = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
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

	return deployContractsWithQuorum(t, endpoint, []client.Address{endorser}, 1, pp0)
}

// deployContractsWithQuorum is deployContracts for an endorser set of any size, so a test can run the
// configuration production actually uses: a threshold above one.
func deployContractsWithQuorum(
	t *testing.T,
	endpoint string,
	endorsers []client.Address,
	threshold int,
	pp0 []byte,
) client.Address {
	t.Helper()

	addresses := make([]string, len(endorsers))
	for i, e := range endorsers {
		addresses[i] = e.Hex()
	}

	// vm.startBroadcast() takes its sender from the CLI, so the key is passed as a flag rather than
	// through the environment.
	cmd := exec.Command("forge", "script", "script/Deploy.s.sol:Deploy",
		"--rpc-url", endpoint, "--broadcast", "--json",
		"--private-key", "0x"+anvilKey1)
	cmd.Dir = "contracts"
	cmd.Env = append(cmd.Environ(),
		"EVM_ENDORSERS="+strings.Join(addresses, ","),
		"EVM_THRESHOLD="+strconv.Itoa(threshold),
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
	// The endorser set has to be the one actually seeded into the deployed verifier. validConfig's
	// placeholder address is not, and Connect's policy check reads the real thing off the chain.
	cfg.Endorsement.Endorsers[0].Address = signer.Address().Hex()
	cfg.Finality.BlockTag = client.BlockTagLatest // anvil mines instantly; there is no finalized tag
	cfg.Finality.PollInterval = 50 * time.Millisecond
	cfg.Finality.Timeout = 15 * time.Second
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate())

	submitter, err := NewSubmitter(evmClient, key, tokenState, big.NewInt(testChainID), cfg.Gas)
	require.NoError(t, err)

	n, err := NewNetwork("evm-net", evmClient, []NamespaceConfig{{Namespace: "token", Config: cfg, Submitter: submitter}}, nil, nil)
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
	issueEnv := &Envelope{Anchor: issueAnchorID, Namespace: "token", Delta: issued, Endorsements: endorse(t, signer, domain, issued)}

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
		Namespace:    "token",
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
		Namespace:    "token",
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
	nsBinding, err := n.binding("token")
	require.NoError(t, err)
	recipientHash, found, err := nsBinding.finality.TxHashByAnchor(t.Context(), issueAnchor)
	require.NoError(t, err)
	require.True(t, found, "a recipient must be able to resolve an applied anchor from the chain alone")
	assert.Equal(t, issueEnv.EthTxHash, recipientHash.Hex(),
		"the hash from the log metadata must be the transaction that applied the anchor")

	// An anchor that was never applied yields nothing, rather than an error: it may simply be pending.
	_, found, err = nsBinding.finality.TxHashByAnchor(t.Context(), anchorOf(t, n, "never-submitted"))
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

// TestRevertClassificationAgainstAnvil pins the permanent/transient split against a real node.
//
// It exists because the split cannot be trusted to a fake: nodes disagree on how they report a
// revert, and the driver got it wrong for geth and anvil, which use EIP-1474's code 3 rather than the
// -32000 Besu uses. That misread sent a permanently rejected transaction back to the caller as
// ErrNetworkUnavailable, whose documented meaning is "the transaction is untouched, retry with
// backoff" -- so a caller following the contract would retry a doomed transaction forever.
//
// A unit test over a handwritten error body cannot catch a wrong assumption about what nodes actually
// send, which is precisely what the bug was, so this drives a genuine revert out of a genuine node.
func TestRevertClassificationAgainstAnvil(t *testing.T) {
	endpoint := startAnvil(t)

	deployedKeyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)
	deployedSigner, err := eip712.NewSignerFromBytes(deployedKeyBytes)
	require.NoError(t, err)

	// anvilStranger is a real key the deployed verifier does not know, so a bundle it signs is
	// rejected on chain: a genuine revert, reached through the driver's ordinary submission path.
	strangerBytes, err := hex.DecodeString(anvilStrangerKey)
	require.NoError(t, err)
	stranger, err := eip712.NewSignerFromBytes(strangerBytes)
	require.NoError(t, err)

	pp0 := []byte("revert-classification-params")
	tokenState := deployContracts(t, endpoint, deployedSigner.Address(), pp0)

	evmClient, err := client.NewJSONRPCClient(endpoint, nil)
	require.NoError(t, err)

	cfg := validConfig()
	cfg.Endpoint = endpoint
	cfg.Contracts.TokenState = tokenState.Hex()
	cfg.Finality.BlockTag = client.BlockTagLatest
	cfg.Endorsement.Endorsers[0].Address = deployedSigner.Address().Hex()
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate())

	submitter, err := NewSubmitter(
		evmClient, secp256k1.PrivKeyFromBytes(deployedKeyBytes), tokenState, big.NewInt(testChainID), cfg.Gas)
	require.NoError(t, err)
	n, err := NewNetwork("evm-net", evmClient, []NamespaceConfig{{Namespace: "token", Config: cfg, Submitter: submitter}}, nil, nil)
	require.NoError(t, err)

	anchorID := n.ComputeTxID(&driver.TxID{Creator: []byte("issuer")})
	anchor, err := keys.AnchorFromTxID(anchorID)
	require.NoError(t, err)
	tokenData := []byte("rejected-token")
	delta := &statedelta.StateDelta{
		Anchor: anchor,
		Outputs: []statedelta.OutputToken{{
			TokenID:   keys.ComputeTokenID(anchor, 0),
			SNMarker:  keys.OutputSNMarker(anchor, 0, tokenData),
			TokenData: tokenData,
		}},
		TokenRequestHash:    sha256Of("issue-request"),
		PublicParamsHash:    sha256Of(string(pp0)),
		PublicParamsVersion: 0,
	}
	domain := eip712.Domain{ChainID: big.NewInt(testChainID), VerifyingContract: tokenState}

	err = n.Broadcast(t.Context(), &Envelope{
		Anchor:       anchorID,
		Namespace:    "token",
		Delta:        delta,
		Endorsements: endorse(t, stranger, domain, delta),
	})

	require.Error(t, err, "the chain must reject a bundle from an unregistered endorser")
	require.ErrorIs(t, err, ErrTransactionReverted,
		"a rejected transaction is permanent: the caller must re-derive it, not resend it")
	assert.NotErrorIs(t, err, ErrNetworkUnavailable,
		"classifying it as transient tells the caller to retry a transaction the chain will reject every time")
}

// lossyProxy forwards every JSON-RPC call to the node but swallows the reply to the first
// eth_sendRawTransaction: the node accepts and mines the transaction, the caller sees a failure.
// That is what a client-side timeout or a dropped connection looks like from the driver's side, and
// it is the one step where "the broadcast failed" is not evidence the chain never took it.
type lossyProxy struct {
	target  string
	swallow atomic.Int32
}

func (p *lossyProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)

		return
	}
	resp, err := http.Post(p.target, "application/json", bytes.NewReader(body))
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)

		return
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)

		return
	}

	if strings.Contains(string(body), "eth_sendRawTransaction") && p.swallow.Add(-1) >= 0 {
		// The node has the transaction. The caller will not hear that.
		w.WriteHeader(http.StatusGatewayTimeout)

		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// TestSubmitterRecoversFromALostBroadcastReply is a production failure reproduced against a real node.
//
// One broadcast whose reply is lost, an ordinary timeout or a dropped connection, used to wedge the
// submitting account permanently. The chain had mined the transaction, so the nonce was spent, but the
// driver saw a failure and kept reissuing that same nonce. Every later transaction failed "nonce too
// low", and because that is also a failed broadcast the manager never re-read the chain to find out.
// Only restarting the process recovered, and the failures were reported as ErrNetworkUnavailable, so a
// caller obeying the contract retried forever.
//
// It runs through a proxy rather than a fake so the node genuinely accepts and mines the transaction
// the driver believes it failed to send. A fake cannot produce that disagreement, which is the whole
// bug.
func TestSubmitterRecoversFromALostBroadcastReply(t *testing.T) {
	node := startAnvil(t)

	proxy := &lossyProxy{target: node}
	proxy.swallow.Store(1)
	srv := httptest.NewServer(proxy)
	t.Cleanup(srv.Close)

	keyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)

	viaProxy, err := client.NewJSONRPCClient(srv.URL, nil)
	require.NoError(t, err)
	direct, err := client.NewJSONRPCClient(node, nil)
	require.NoError(t, err)

	// A plain account as the target, with a fixed gas limit so nothing is estimated: every call is a
	// mined, successful transaction that consumes a nonce, which is all the nonce question needs.
	target, err := client.HexToAddress("0x000000000000000000000000000000000000dEaD")
	require.NoError(t, err)
	submitter, err := NewSubmitter(viaProxy, secp256k1.PrivKeyFromBytes(keyBytes), target,
		big.NewInt(testChainID), GasConfig{Strategy: GasStrategyFixed, Limit: 200_000})
	require.NoError(t, err)

	send := func(n byte) error {
		var anchor [32]byte
		anchor[31] = n
		_, _, err := submitter.Submit(t.Context(), &statedelta.StateDelta{
			Anchor: anchor,
			Outputs: []statedelta.OutputToken{{
				TokenID: keys.ComputeTokenID(anchor, 0), TokenData: []byte{n},
			}},
			TokenRequestHash:    sha256Of("request"),
			PublicParamsHash:    sha256Of("pp"),
			PublicParamsVersion: 0,
		}, [][]byte{make([]byte, 65)})

		return err
	}
	chainNonce := func() uint64 {
		n, err := direct.PendingNonceAt(t.Context(), submitter.Address())
		require.NoError(t, err)

		return n
	}

	require.Error(t, send(0x01), "the swallowed reply must surface as a failed broadcast")
	require.Equal(t, uint64(1), chainNonce(), "but the chain did take the transaction")

	// The account has to keep working. Before the fix every one of these failed "nonce too low".
	for _, n := range []byte{0x02, 0x03, 0x04} {
		require.NoError(t, send(n), "the submitter must recover its nonce from the chain, not wedge")
	}
	assert.Equal(t, uint64(4), chainNonce(), "every later transaction reached the chain")
}

// TestBroadcastRejectionIsClassifiedAgainstAnvil covers the other half of the classification the
// revert test covers for gas estimation: a node that refuses the transaction outright.
//
// A broadcast can fail for two very different reasons. The node may be unreachable, in which case the
// transaction is still good and the caller should retry. Or the node may have looked at the
// transaction and refused it permanently, as it does when the sender cannot pay for the gas. Retrying
// that second kind never succeeds, so reporting it as transient asks the caller to loop forever on a
// transaction the chain will never take.
//
// The submitting account here holds nothing at all, which is the ordinary way this happens in
// production: the account that pays for gas runs dry.
func TestBroadcastRejectionIsClassifiedAgainstAnvil(t *testing.T) {
	node := startAnvil(t)

	evmClient, err := client.NewJSONRPCClient(node, nil)
	require.NoError(t, err)

	// A key anvil has never heard of, so the account holds a zero balance.
	var brokeKey [32]byte
	_, err = rand.Read(brokeKey[:])
	require.NoError(t, err)
	key := secp256k1.PrivKeyFromBytes(brokeKey[:])

	target, err := client.HexToAddress("0x000000000000000000000000000000000000dEaD")
	require.NoError(t, err)

	// A fixed gas limit keeps estimation out of it: the only thing under test is how the node's
	// refusal at broadcast time is classified.
	submitter, err := NewSubmitter(evmClient, key, target, big.NewInt(testChainID),
		GasConfig{Strategy: GasStrategyFixed, Limit: 200_000})
	require.NoError(t, err)

	var anchor [32]byte
	anchor[31] = 0x01
	_, _, err = submitter.Submit(t.Context(), &statedelta.StateDelta{
		Anchor: anchor,
		Outputs: []statedelta.OutputToken{{
			TokenID: keys.ComputeTokenID(anchor, 0), TokenData: []byte{0x01},
		}},
		TokenRequestHash:    sha256Of("request"),
		PublicParamsHash:    sha256Of("pp"),
		PublicParamsVersion: 0,
	}, [][]byte{make([]byte, 65)})

	require.Error(t, err)
	t.Logf("node said: %v", err)

	assert.False(t, errors.Is(err, ErrNetworkUnavailable),
		"an account that cannot pay for gas is a permanent refusal, not an unreachable node: "+
			"reporting it as transient makes the caller retry a transaction the chain will never take")
	assert.True(t, errors.Is(err, ErrTransactionRejected), "and it is the node declining to carry it")
	assert.True(t, errors.Is(err, ErrTransactionReverted),
		"joined with the original permanent class, so a caller that knows only those two still maps it to Invalid")
	assert.False(t, errors.Is(err, ErrNonceMayBeConsumed),
		"a transaction the node never accepted cannot have consumed a nonce")

	// The node's own words have to survive: they are what an operator debugs from, and "insufficient
	// funds" is the difference between topping up an account and looking for a bug.
	assert.Contains(t, strings.ToLower(err.Error()), "insufficient funds")
}

// TestResubmittingAnAppliedAnchorAgainstAnvil covers what happens after the failure the lost-reply
// test recovers from: the broadcast reply went missing, the caller does not know the transaction
// landed, and it retries the same delta.
//
// The second attempt has to be refused, because the anchor is already applied and applying it twice
// would be a double spend. What matters is the answer the caller is given for it. The transaction it
// was actually asking about succeeded, so telling it the request is invalid would have it discard a
// token transfer that is on chain and final.
func TestResubmittingAnAppliedAnchorAgainstAnvil(t *testing.T) {
	endpoint := startAnvil(t)

	keyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)
	key := secp256k1.PrivKeyFromBytes(keyBytes)
	signer, err := eip712.NewSignerFromBytes(keyBytes)
	require.NoError(t, err)

	pp0 := []byte("replay-public-params")
	tokenState := deployContracts(t, endpoint, signer.Address(), pp0)

	evmClient, err := client.NewJSONRPCClient(endpoint, nil)
	require.NoError(t, err)

	cfg := validConfig()
	cfg.Endpoint = endpoint
	cfg.Contracts.TokenState = tokenState.Hex()
	cfg.Endorsement.Endorsers[0].Address = signer.Address().Hex()
	cfg.Finality.BlockTag = client.BlockTagLatest
	cfg.Finality.PollInterval = 50 * time.Millisecond
	cfg.Finality.Timeout = 15 * time.Second
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate())

	submitter, err := NewSubmitter(evmClient, key, tokenState, big.NewInt(testChainID), cfg.Gas)
	require.NoError(t, err)
	n, err := NewNetwork("evm-net", evmClient, []NamespaceConfig{{Namespace: "token", Config: cfg, Submitter: submitter}}, nil, nil)
	require.NoError(t, err)
	_, err = n.Connect("token")
	require.NoError(t, err)

	domain := eip712.Domain{ChainID: big.NewInt(testChainID), VerifyingContract: tokenState}

	anchorID := n.ComputeTxID(&driver.TxID{Creator: []byte("issuer-replay")})
	anchor, err := keys.AnchorFromTxID(anchorID)
	require.NoError(t, err)

	tokenData := []byte("alice-owns-100")
	delta := &statedelta.StateDelta{
		Anchor: anchor,
		Outputs: []statedelta.OutputToken{{
			TokenID:   keys.ComputeTokenID(anchor, 0),
			SNMarker:  keys.OutputSNMarker(anchor, 0, tokenData),
			TokenData: tokenData,
		}},
		TokenRequestHash:    sha256Of("replay-request"),
		PublicParamsHash:    sha256Of(string(pp0)),
		PublicParamsVersion: 0,
	}

	first := &Envelope{Anchor: anchorID, Namespace: "token", Delta: delta, Endorsements: endorse(t, signer, domain, delta)}
	require.NoError(t, n.Broadcast(t.Context(), first), "the first apply must land")
	require.Equal(t, uint64(1), waitMined(t, evmClient, first.EthTxHash), "and must not revert")

	// The anchor is now on chain. This is the state the caller cannot see when its reply was lost.
	code, _, _, err := n.GetTransactionStatus(t.Context(), "token", anchorID)
	require.NoError(t, err)
	require.Equal(t, driver.Valid, code, "the chain considers this transaction applied")

	// Now the retry the caller would make.
	second := &Envelope{Anchor: anchorID, Namespace: "token", Delta: delta, Endorsements: endorse(t, signer, domain, delta)}
	require.NoError(t, n.Broadcast(t.Context(), second),
		"the anchor this caller asked about is applied and final, so its transaction succeeded: "+
			"reporting the request as invalid would have it discard a transfer that is on chain")

	// The chain must be untouched by the retry: refusing the second apply is what stops the double
	// spend, and treating it as a success must not paper over an apply that actually went through.
	code, _, _, err = n.GetTransactionStatus(t.Context(), "token", anchorID)
	require.NoError(t, err)
	assert.Equal(t, driver.Valid, code)

	spent, err := n.AreTokensSpent(t.Context(), "token", []*token.ID{{TxId: anchorID, Index: 0}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []bool{false}, spent, "the token must still be unspent: the retry applied nothing")
}

// TestConnectRejectsAnUndeployedTokenStateAgainstAnvil covers the most ordinary way an EVM
// deployment is misconfigured: contracts.tokenState points somewhere there is no contract. A typo, a
// config copied between environments, or a node pointed at a chain where the deploy has not happened
// yet all land here.
//
// eth_call against an address with no code is not an error on any node: it returns empty data. Every
// read the driver makes therefore comes back empty rather than failing, so nothing on the startup
// path notices, and the node comes up looking healthy. The cost is paid later, once per transaction,
// as failures that do not look like configuration problems from the outside.
func TestConnectRejectsAnUndeployedTokenStateAgainstAnvil(t *testing.T) {
	endpoint := startAnvil(t)

	evmClient, err := client.NewJSONRPCClient(endpoint, nil)
	require.NoError(t, err)

	// A well-formed address that anvil has never deployed anything to.
	empty, err := client.HexToAddress("0x00000000000000000000000000000000deadbeef")
	require.NoError(t, err)

	cfg := validConfig()
	cfg.Endpoint = endpoint
	cfg.Contracts.TokenState = empty.Hex()
	cfg.Finality.BlockTag = client.BlockTagLatest
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate(), "the configuration is well formed; only the chain disagrees")

	n, err := NewNetwork("evm-net", evmClient, []NamespaceConfig{{Namespace: "token", Config: cfg, Submitter: nil}}, nil, nil)
	require.NoError(t, err)

	_, err = n.Connect("token")
	require.Error(t, err,
		"a node whose tokenState address holds no contract cannot serve this TMS, and saying so at "+
			"startup is the whole point of having startup checks")
	t.Logf("Connect said: %v", err)
}

// anvilKey3 is anvil's third well-known development account, needed once a test wants an endorser set
// bigger than two.
const anvilKey3 = "5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"

// TestQuorumEndorsementAgainstAnvil runs the configuration production actually uses and the single
// end-to-end test does not: a threshold above one, and a delta carrying more than one output.
//
// Both are places where an encoding mistake hides. applyStateDelta takes the signatures as a dynamic
// array of dynamic bytes, and an array of length one is the case where a wrong offset still happens to
// land correctly; the same is true of the delta's own output array. A quorum of two over two outputs
// is the smallest shape that would expose either, and the contract is the judge.
func TestQuorumEndorsementAgainstAnvil(t *testing.T) {
	endpoint := startAnvil(t)

	signers := make([]*eip712.Signer, 0, 3)
	addresses := make([]client.Address, 0, 3)
	for _, k := range []string{anvilKey1, anvilStrangerKey, anvilKey3} {
		raw, err := hex.DecodeString(k)
		require.NoError(t, err)
		signer, err := eip712.NewSignerFromBytes(raw)
		require.NoError(t, err)
		signers = append(signers, signer)
		addresses = append(addresses, signer.Address())
	}

	pp0 := []byte("quorum-public-params")
	tokenState := deployContractsWithQuorum(t, endpoint, addresses, 2, pp0)

	evmClient, err := client.NewJSONRPCClient(endpoint, nil)
	require.NoError(t, err)

	keyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)

	cfg := validConfig()
	cfg.Endpoint = endpoint
	cfg.Contracts.TokenState = tokenState.Hex()
	cfg.Endorsement.Threshold = 2
	cfg.Endorsement.Endorsers = []EndorserBinding{
		{Address: addresses[0].Hex(), FSCIdentity: "endorser-1"},
		{Address: addresses[1].Hex(), FSCIdentity: "endorser-2"},
		{Address: addresses[2].Hex(), FSCIdentity: "endorser-3"},
	}
	cfg.Finality.BlockTag = client.BlockTagLatest
	cfg.Finality.PollInterval = 50 * time.Millisecond
	cfg.Finality.Timeout = 15 * time.Second
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate())

	submitter, err := NewSubmitter(evmClient, secp256k1.PrivKeyFromBytes(keyBytes), tokenState,
		big.NewInt(testChainID), cfg.Gas)
	require.NoError(t, err)
	n, err := NewNetwork("evm-net", evmClient, []NamespaceConfig{{Namespace: "token", Config: cfg, Submitter: submitter}}, nil, nil)
	require.NoError(t, err)

	// Connect also proves the policy check reads a three-endorser set and a threshold of two off the
	// deployed verifier and agrees with the configuration.
	_, err = n.Connect("token")
	require.NoError(t, err, "the configured quorum must match the one the verifier was deployed with")

	domain := eip712.Domain{ChainID: big.NewInt(testChainID), VerifyingContract: tokenState}

	anchorID := n.ComputeTxID(&driver.TxID{Creator: []byte("quorum-issuer")})
	anchor, err := keys.AnchorFromTxID(anchorID)
	require.NoError(t, err)

	firstData, secondData := []byte("alice-owns-60"), []byte("bob-owns-40")
	delta := &statedelta.StateDelta{
		Anchor: anchor,
		Outputs: []statedelta.OutputToken{
			{
				TokenID:   keys.ComputeTokenID(anchor, 0),
				SNMarker:  keys.OutputSNMarker(anchor, 0, firstData),
				TokenData: firstData,
			},
			{
				TokenID:   keys.ComputeTokenID(anchor, 1),
				SNMarker:  keys.OutputSNMarker(anchor, 1, secondData),
				TokenData: secondData,
			},
		},
		TokenRequestHash:    sha256Of("quorum-request"),
		PublicParamsHash:    sha256Of(string(pp0)),
		PublicParamsVersion: 0,
	}

	digest := eip712.Digest(domain, delta)
	first, err := signers[0].Sign(digest)
	require.NoError(t, err)
	third, err := signers[2].Sign(digest)
	require.NoError(t, err)

	env := &Envelope{Anchor: anchorID, Namespace: "token", Delta: delta, Endorsements: [][]byte{first, third}}
	require.NoError(t, n.Broadcast(t.Context(), env), "a genuine 2-of-3 quorum must be accepted")
	require.Equal(t, uint64(1), waitMined(t, evmClient, env.EthTxHash), "and must not revert")

	// Both outputs have to have landed, in the right order: a wrong offset in the output array would
	// show up here as swapped or truncated token bytes rather than as a revert.
	stored, err := n.QueryTokens(t.Context(), "token", []*token.ID{
		{TxId: anchorID, Index: 0},
		{TxId: anchorID, Index: 1},
	})
	require.NoError(t, err)
	require.Len(t, stored, 2)
	assert.Equal(t, firstData, stored[0])
	assert.Equal(t, secondData, stored[1])
}

// TestConcurrentSubmissionsAgainstAnvil drives the submitter the way a busy node does: several token
// transactions in flight at once, all paying gas from the one account.
//
// Nonces are the thing that breaks here. They are per account and strictly sequential, so two
// transactions that pick the same one mean the second is refused, and a gap means every transaction
// after it sits in the mempool unmined. Neither shows up in a test that submits one at a time.
func TestConcurrentSubmissionsAgainstAnvil(t *testing.T) {
	node := startAnvil(t)

	evmClient, err := client.NewJSONRPCClient(node, nil)
	require.NoError(t, err)

	keyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)

	target, err := client.HexToAddress("0x000000000000000000000000000000000000dEaD")
	require.NoError(t, err)
	submitter, err := NewSubmitter(evmClient, secp256k1.PrivKeyFromBytes(keyBytes), target,
		big.NewInt(testChainID), GasConfig{Strategy: GasStrategyFixed, Limit: 200_000})
	require.NoError(t, err)

	// Marked by byte rather than by loop index so each anchor is distinct without converting an int.
	markers := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	parallel := len(markers)
	var wg sync.WaitGroup
	hashes := make([]client.Hash, parallel)
	errs := make([]error, parallel)

	for i, marker := range markers {
		wg.Go(func() {
			var anchor [32]byte
			anchor[31] = marker
			_, hash, err := submitter.Submit(t.Context(), &statedelta.StateDelta{
				Anchor: anchor,
				Outputs: []statedelta.OutputToken{{
					TokenID: keys.ComputeTokenID(anchor, 0), TokenData: []byte{marker},
				}},
				TokenRequestHash:    sha256Of("concurrent-request"),
				PublicParamsHash:    sha256Of("pp"),
				PublicParamsVersion: 0,
			}, [][]byte{make([]byte, 65)})
			hashes[i], errs[i] = hash, err
		})
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "submission %d failed", i)
	}

	// Every one has to be a distinct transaction that the chain actually took. A reused nonce would
	// show up as two identical hashes, or as one of these never being mined.
	seen := make(map[client.Hash]struct{}, parallel)
	for i, h := range hashes {
		_, dup := seen[h]
		require.False(t, dup, "submission %d reused another transaction's hash", i)
		seen[h] = struct{}{}
		assert.Equal(t, uint64(1), waitMined(t, evmClient, h.Hex()), "submission %d must be mined", i)
	}

	// And the account's nonce has to have advanced by exactly the number of transactions: a gap would
	// mean the chain is holding a nonce nothing will ever fill.
	next, err := evmClient.PendingNonceAt(t.Context(), submitter.Address())
	require.NoError(t, err)
	assert.Equal(t, uint64(len(markers)), next, "the nonce sequence must be dense, with no gaps")
}

// TestMinedButRevertedAgainstAnvil covers the race the estimate cannot close. Gas estimation runs
// against the state at the time it is asked; the transaction executes against the state when it is
// mined. Anything that changes in between, another spend of the same input above all, means a
// transaction that estimated cleanly reverts on chain.
//
// A fixed gas limit reproduces that deterministically by skipping estimation entirely, which is also
// a supported production configuration. What must never happen is the driver reporting such a
// transaction as applied: the apply reverted, so nothing was written, and a caller told otherwise
// would credit tokens the chain does not have.
func TestMinedButRevertedAgainstAnvil(t *testing.T) {
	endpoint := startAnvil(t)

	keyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)
	signer, err := eip712.NewSignerFromBytes(keyBytes)
	require.NoError(t, err)

	pp0 := []byte("reverted-public-params")
	tokenState := deployContracts(t, endpoint, signer.Address(), pp0)

	evmClient, err := client.NewJSONRPCClient(endpoint, nil)
	require.NoError(t, err)

	cfg := validConfig()
	cfg.Endpoint = endpoint
	cfg.Contracts.TokenState = tokenState.Hex()
	cfg.Endorsement.Endorsers[0].Address = signer.Address().Hex()
	cfg.Finality.BlockTag = client.BlockTagLatest
	cfg.Finality.PollInterval = 50 * time.Millisecond
	cfg.Finality.Timeout = 2 * time.Second
	// Fixed gas skips estimation, so the transaction reaches the chain and reverts there rather than
	// being caught before it is sent.
	cfg.Gas = GasConfig{Strategy: GasStrategyFixed, Limit: 500_000}
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate())

	submitter, err := NewSubmitter(evmClient, secp256k1.PrivKeyFromBytes(keyBytes), tokenState,
		big.NewInt(testChainID), cfg.Gas)
	require.NoError(t, err)
	n, err := NewNetwork("evm-net", evmClient, []NamespaceConfig{{Namespace: "token", Config: cfg, Submitter: submitter}}, nil, nil)
	require.NoError(t, err)
	_, err = n.Connect("token")
	require.NoError(t, err)

	anchorID := n.ComputeTxID(&driver.TxID{Creator: []byte("doomed")})
	anchor, err := keys.AnchorFromTxID(anchorID)
	require.NoError(t, err)

	tokenData := []byte("never-applied")
	delta := &statedelta.StateDelta{
		Anchor: anchor,
		Outputs: []statedelta.OutputToken{{
			TokenID:   keys.ComputeTokenID(anchor, 0),
			SNMarker:  keys.OutputSNMarker(anchor, 0, tokenData),
			TokenData: tokenData,
		}},
		TokenRequestHash:    sha256Of("doomed-request"),
		PublicParamsHash:    sha256Of(string(pp0)),
		PublicParamsVersion: 0,
	}

	// A signature from a key the verifier does not know: the contract rejects it at apply time, which
	// stands in for any reason the state moved under the transaction.
	strangerBytes, err := hex.DecodeString(anvilStrangerKey)
	require.NoError(t, err)
	stranger, err := eip712.NewSignerFromBytes(strangerBytes)
	require.NoError(t, err)
	domain := eip712.Domain{ChainID: big.NewInt(testChainID), VerifyingContract: tokenState}

	env := &Envelope{Anchor: anchorID, Namespace: "token", Delta: delta, Endorsements: endorse(t, stranger, domain, delta)}
	require.NoError(t, n.Broadcast(t.Context(), env), "with a fixed gas limit nothing rejects it before it is sent")
	require.Equal(t, uint64(0), waitMined(t, evmClient, env.EthTxHash), "the apply must revert on chain")

	// The chain wrote nothing, so the anchor must not read as applied, and the token must not exist.
	code, _, _, err := n.GetTransactionStatus(t.Context(), "token", anchorID)
	require.NoError(t, err)
	assert.NotEqual(t, driver.Valid, code,
		"a reverted apply wrote nothing: reporting it as valid would credit tokens the chain does not have")

	_, err = n.QueryTokens(t.Context(), "token", []*token.ID{{TxId: anchorID, Index: 0}})
	assert.Error(t, err, "the output of a reverted apply must not be readable")
}

// TestDigestAgreesWithTheContractAgainstAnvil is a differential test of the two EIP-712
// implementations. The endorsers sign a digest Go computes; the contract recomputes it from the
// calldata and checks the signatures against its own. If the two ever disagree about how a delta
// hashes, the contract rejects a perfectly good quorum with InvalidSignatures, and the only symptom
// is that transactions of that shape stop working.
//
// The end-to-end test agrees for the one shape it builds. These are the shapes it does not: a delta
// carrying transfer metadata, which is what an HTLC claim looks like, several metadata entries in
// canonical order, and an output with empty token data. Each is submitted for real, so the contract
// is the judge of whether the digests match.
func TestDigestAgreesWithTheContractAgainstAnvil(t *testing.T) {
	endpoint := startAnvil(t)

	keyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)
	signer, err := eip712.NewSignerFromBytes(keyBytes)
	require.NoError(t, err)

	pp0 := []byte("digest-public-params")
	tokenState := deployContracts(t, endpoint, signer.Address(), pp0)

	evmClient, err := client.NewJSONRPCClient(endpoint, nil)
	require.NoError(t, err)

	cfg := validConfig()
	cfg.Endpoint = endpoint
	cfg.Contracts.TokenState = tokenState.Hex()
	cfg.Endorsement.Endorsers[0].Address = signer.Address().Hex()
	cfg.Finality.BlockTag = client.BlockTagLatest
	cfg.Finality.PollInterval = 50 * time.Millisecond
	cfg.Finality.Timeout = 15 * time.Second
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate())

	submitter, err := NewSubmitter(evmClient, secp256k1.PrivKeyFromBytes(keyBytes), tokenState,
		big.NewInt(testChainID), cfg.Gas)
	require.NoError(t, err)
	n, err := NewNetwork("evm-net", evmClient, []NamespaceConfig{{Namespace: "token", Config: cfg, Submitter: submitter}}, nil, nil)
	require.NoError(t, err)
	_, err = n.Connect("token")
	require.NoError(t, err)

	domain := eip712.Domain{ChainID: big.NewInt(testChainID), VerifyingContract: tokenState}

	for _, tc := range []struct {
		name  string
		build func(anchor [32]byte) *statedelta.StateDelta
	}{
		{
			name: "one metadata entry",
			build: func(anchor [32]byte) *statedelta.StateDelta {
				data := []byte("owns-10")

				return &statedelta.StateDelta{
					Anchor: anchor,
					Outputs: []statedelta.OutputToken{{
						TokenID:   keys.ComputeTokenID(anchor, 0),
						SNMarker:  keys.OutputSNMarker(anchor, 0, data),
						TokenData: data,
					}},
					MetadataKeys: [][32]byte{keys.TransferMetadataKey("htlc-claim-1")},
					MetadataVals: [][]byte{[]byte("preimage")},
				}
			},
		},
		{
			name: "several metadata entries in canonical order",
			build: func(anchor [32]byte) *statedelta.StateDelta {
				keysOut := [][32]byte{
					keys.TransferMetadataKey("a"),
					keys.TransferMetadataKey("b"),
					keys.TransferMetadataKey("c"),
				}
				slices.SortFunc(keysOut, func(x, y [32]byte) int { return bytes.Compare(x[:], y[:]) })

				return &statedelta.StateDelta{
					Anchor:       anchor,
					MetadataKeys: keysOut,
					MetadataVals: [][]byte{[]byte("one"), []byte(""), []byte("three")},
				}
			},
		},
		{
			name: "an output with empty token data",
			build: func(anchor [32]byte) *statedelta.StateDelta {
				return &statedelta.StateDelta{
					Anchor: anchor,
					Outputs: []statedelta.OutputToken{{
						TokenID:  keys.ComputeTokenID(anchor, 0),
						SNMarker: keys.OutputSNMarker(anchor, 0, nil),
					}},
				}
			},
		},
		{
			name: "no outputs and no spends at all",
			build: func(anchor [32]byte) *statedelta.StateDelta {
				return &statedelta.StateDelta{Anchor: anchor}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchorID := n.ComputeTxID(&driver.TxID{Creator: []byte(tc.name)})
			anchor, err := keys.AnchorFromTxID(anchorID)
			require.NoError(t, err)

			delta := tc.build(anchor)
			delta.TokenRequestHash = sha256Of(tc.name)
			delta.PublicParamsHash = sha256Of(string(pp0))
			delta.PublicParamsVersion = 0
			require.NoError(t, delta.Validate(), "the test itself must build a well-formed delta")

			env := &Envelope{
				Anchor:       anchorID,
				Namespace:    "token",
				Delta:        delta,
				Endorsements: endorse(t, signer, domain, delta),
			}
			require.NoError(t, n.Broadcast(t.Context(), env),
				"the contract must recompute the same digest Go signed")
			require.Equal(t, uint64(1), waitMined(t, evmClient, env.EthTxHash),
				"an InvalidSignatures revert here means the two EIP-712 implementations disagree")
		})
	}
}

// TestTransferMetadataRoundTripAgainstAnvil follows a metadata entry all the way out and back: the
// endorsed delta writes it, and the reader the token layer uses finds it again. This is the path an
// HTLC claim takes, and nothing exercised it against a real contract.
func TestTransferMetadataRoundTripAgainstAnvil(t *testing.T) {
	endpoint := startAnvil(t)

	keyBytes, err := hex.DecodeString(anvilKey1)
	require.NoError(t, err)
	signer, err := eip712.NewSignerFromBytes(keyBytes)
	require.NoError(t, err)

	pp0 := []byte("metadata-public-params")
	tokenState := deployContracts(t, endpoint, signer.Address(), pp0)

	evmClient, err := client.NewJSONRPCClient(endpoint, nil)
	require.NoError(t, err)

	cfg := validConfig()
	cfg.Endpoint = endpoint
	cfg.Contracts.TokenState = tokenState.Hex()
	cfg.Endorsement.Endorsers[0].Address = signer.Address().Hex()
	cfg.Finality.BlockTag = client.BlockTagLatest
	cfg.Finality.PollInterval = 50 * time.Millisecond
	cfg.Finality.Timeout = 15 * time.Second
	cfg.applyDefaults()
	require.NoError(t, cfg.Validate())

	submitter, err := NewSubmitter(evmClient, secp256k1.PrivKeyFromBytes(keyBytes), tokenState,
		big.NewInt(testChainID), cfg.Gas)
	require.NoError(t, err)
	n, err := NewNetwork("evm-net", evmClient, []NamespaceConfig{{Namespace: "token", Config: cfg, Submitter: submitter}}, nil, nil)
	require.NoError(t, err)
	_, err = n.Connect("token")
	require.NoError(t, err)

	domain := eip712.Domain{ChainID: big.NewInt(testChainID), VerifyingContract: tokenState}

	anchorID := n.ComputeTxID(&driver.TxID{Creator: []byte("metadata-writer")})
	anchor, err := keys.AnchorFromTxID(anchorID)
	require.NoError(t, err)

	present, absent := "htlc-claim-present", "htlc-claim-empty"

	// Canonical order is by key, and each value travels with its own key, exactly as the translator
	// sorts them.
	type entry struct {
		key [32]byte
		val []byte
	}
	pairs := []entry{
		{keys.TransferMetadataKey(present), []byte("the-preimage")},
		{keys.TransferMetadataKey(absent), []byte{}},
	}
	slices.SortStableFunc(pairs, func(a, b entry) int { return bytes.Compare(a.key[:], b.key[:]) })

	delta := &statedelta.StateDelta{
		Anchor:              anchor,
		MetadataKeys:        [][32]byte{pairs[0].key, pairs[1].key},
		MetadataVals:        [][]byte{pairs[0].val, pairs[1].val},
		TokenRequestHash:    sha256Of("metadata-request"),
		PublicParamsHash:    sha256Of(string(pp0)),
		PublicParamsVersion: 0,
	}
	require.NoError(t, delta.Validate())

	env := &Envelope{Anchor: anchorID, Namespace: "token", Delta: delta, Endorsements: endorse(t, signer, domain, delta)}
	require.NoError(t, n.Broadcast(t.Context(), env))
	require.Equal(t, uint64(1), waitMined(t, evmClient, env.EthTxHash), "the metadata write must not revert")

	value, err := n.LookupTransferMetadataKey("token", present, 3*time.Second)
	require.NoError(t, err, "a metadata value written on chain must be readable by the key it was written under")
	assert.Equal(t, []byte("the-preimage"), value)

	// A metadata entry written with an empty value cannot be read back. The apply above did not
	// revert, so the contract accepted the entry and set metadataExists for its key, but that mapping
	// has no getter: getTransferMetadata returns the value alone, and an empty one is what an unset
	// key returns too. LookupTransferMetadataKey therefore polls until it gives up.
	//
	// No shipped token driver writes an empty metadata value, so nothing hits this today. It is pinned
	// here because the failure is silent on the write side and only shows up as a reader timeout much
	// later, and because closing it properly means exposing metadataExists from the contract rather
	// than anything that can be done in Go.
	// The message is deliberately not asserted. LookupTransferMetadataKey shares one deadline between
	// the poll loop and the calls it makes, so whether the caller sees "not found within ..." or the
	// underlying call's own deadline error depends on which side of the loop the timeout lands on.
	_, err = n.LookupTransferMetadataKey("token", absent, 2*time.Second)
	require.Error(t, err, "an empty metadata value is indistinguishable from an absent key on the read path")
	t.Logf("reading back the empty-valued key gave: %v", err)
}
