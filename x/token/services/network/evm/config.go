/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

import (
	"math/big"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"

	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/client"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/eip712"
	"github.com/LFDT-Panurus/panurus/x/token/services/network/evm/endorsement"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// Configuration defaults. They are applied at load time so a minimal config (endpoint, chain id,
// contracts, endorsement) is enough to run, and every unset knob has a documented, safe value.
const (
	// DefaultBlockTag is the block tag state reads use: the PoS finalized tag, which removes reorg
	// handling from v1 (design §7.2). It aliases the canonical value in the client package.
	DefaultBlockTag = client.BlockTagFinalized
	// DefaultPollInterval is how often finality polls a transaction's status.
	DefaultPollInterval = 2 * time.Second
	// DefaultFinalityTimeout bounds how long a transaction is awaited before it is treated as failed.
	// It carries real margin over MinFinalizedTagTimeout (design §7.2, §7.5) so a deployment running
	// on defaults alone is not sitting at the edge of normal PoS finalization variance.
	DefaultFinalityTimeout = 20 * time.Minute
	// MinFinalizedTagTimeout is the floor Validate enforces on Finality.Timeout when BlockTag is
	// finalized. Real time-to-finality on a PoS chain is ~13 minutes (design §7.2); a shorter timeout
	// cannot ever see a transaction finalize and condemns it regardless of whether it succeeded (design
	// §7.5: "any deployment must configure finality.timeout above ... the chain's finality"). It bounds
	// only the chain's own lag; a deployment that also delays broadcasting a signed transaction needs
	// additional headroom on top of this, which Validate has no way to know and cannot enforce.
	MinFinalizedTagTimeout = 13 * time.Minute
	// DefaultGasMultiplier scales the node's gas estimate to absorb small state changes between
	// estimation and execution.
	DefaultGasMultiplier = 1.2
	// DefaultGasStrategy estimates gas per transaction rather than using a fixed limit.
	DefaultGasStrategy = GasStrategyEstimate
)

// Gas strategies.
const (
	// GasStrategyEstimate asks the node to estimate gas and scales it by Multiplier.
	GasStrategyEstimate = "estimate"
	// GasStrategyFixed uses Limit for every transaction.
	GasStrategyFixed = "fixed"
)

// Config is the EVM network configuration for one TMS, read from
// token.tms.<tms-id>.services.network.evm (design §10).
type Config struct {
	// Endpoint is the node's JSON-RPC URL.
	Endpoint string `yaml:"endpoint"`
	// ChainID is the chain the driver signs for; it is checked against the node at startup so a
	// misconfiguration surfaces immediately rather than as rejected transactions.
	ChainID int64 `yaml:"chainID"`

	Contracts   ContractsConfig   `yaml:"contracts"`
	Finality    FinalityConfig    `yaml:"finality"`
	Gas         GasConfig         `yaml:"gas"`
	Endorser    EndorserConfig    `yaml:"endorser"`
	Submitter   SubmitterConfig   `yaml:"submitter"`
	Endorsement EndorsementConfig `yaml:"endorsement"`
}

// ContractsConfig holds the deployed contract addresses for the TMS.
type ContractsConfig struct {
	// TokenState is this TMS's TokenState clone; it also anchors the EIP-712 domain.
	TokenState string `yaml:"tokenState"`
	// EndorsementVerifier is the verifier the TokenState calls; optional in config, since the
	// TokenState holds the authoritative reference.
	EndorsementVerifier string `yaml:"endorsementVerifier"`
}

// FinalityConfig controls how transaction finality is observed.
type FinalityConfig struct {
	// BlockTag is the tag state is read at: finalized (default, no reorg risk), safe, or latest (no
	// reorg protection at all; only appropriate for a local, instant-mining chain).
	BlockTag string `yaml:"blockTag"`
	// PollInterval is the delay between status polls.
	PollInterval time.Duration `yaml:"pollInterval"`
	// Timeout bounds how long a transaction is awaited. It is also a recipient's only failure signal:
	// a failed apply reverts and emits no log, so "no event by the timeout" is what makes it Invalid
	// (design §7.4).
	Timeout time.Duration `yaml:"timeout"`
	// FromBlock is where log searches start when resolving an anchor to its transaction hash. It
	// defaults to zero, the whole chain, which is right for a freshly bootstrapped network; on a chain
	// where the TokenState was deployed much later, set it to the deployment block so the search does
	// not walk history that cannot contain the event.
	FromBlock uint64 `yaml:"fromBlock"`
}

// GasConfig controls gas limit selection.
type GasConfig struct {
	// Strategy is estimate or fixed.
	Strategy string `yaml:"strategy"`
	// Multiplier scales the node's estimate when Strategy is estimate.
	Multiplier float64 `yaml:"multiplier"`
	// Limit is the gas limit used when Strategy is fixed.
	Limit uint64 `yaml:"limit"`
}

// EndorserConfig describes this node's endorser identity, present only if the node endorses.
type EndorserConfig struct {
	// Enabled marks this node as an endorser.
	Enabled bool `yaml:"enabled"`
	// Keystore is the path to this endorser's secp256k1 key material.
	Keystore string `yaml:"keystore"`
	// Address is the endorser's Ethereum address, as registered in the EndorsementVerifier.
	Address string `yaml:"address"`
	// FSCIdentity is the endorser's FSC identity, used to route endorsement requests.
	FSCIdentity string `yaml:"fscIdentity"`
}

// SubmitterConfig describes the account that signs and pays for transactions.
type SubmitterConfig struct {
	// Keystore is the path to the submitter's secp256k1 key material.
	Keystore string `yaml:"keystore"`
	// Address is the submitter's Ethereum address.
	Address string `yaml:"address"`
}

// EndorsementConfig is the endorsement policy: the quorum, who may ask for endorsement, and the
// endorser set with the address to FSC identity binding the initiator routes on (design §6.1).
type EndorsementConfig struct {
	// Threshold is the number of distinct endorser signatures a transaction needs. It must match the
	// threshold the EndorsementVerifier was constructed with.
	Threshold uint `yaml:"threshold"`
	// Allowlist is the FSC identities permitted to request endorsement. It is fail-closed: when this
	// node is configured to endorse, an empty allowlist is a validation error rather than a default
	// that resolves to anyone. There is no automatic "the TMS network's nodes" fallback; every
	// permitted requester has to be named.
	Allowlist []string `yaml:"allowlist"`
	// Endorsers binds each endorser's Ethereum address to its FSC identity.
	Endorsers []EndorserBinding `yaml:"endorsers"`
}

// EndorserBinding is one endorser's address and FSC identity.
type EndorserBinding struct {
	Address     string `yaml:"address"`
	FSCIdentity string `yaml:"fscIdentity"`
}

// LoadConfig reads the EVM configuration from the TMS configuration, applies defaults, and validates
// it. It fails fast: a bad configuration is a startup error (design §13), never a surprise at the
// first transaction.
func LoadConfig(configuration Configuration) (*Config, error) {
	var c Config
	if err := configuration.UnmarshalKey(EVMConfigKey, &c); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal evm configuration under [%s]", EVMConfigKey)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}

	return &c, nil
}

// Configuration is the slice of the SDK's configuration service this package needs.
type Configuration interface {
	// IsSet reports whether a key is defined.
	IsSet(key string) bool
	// UnmarshalKey decodes the value at key into rawVal.
	UnmarshalKey(key string, rawVal any) error
}

// applyDefaults fills unset optional fields.
func (c *Config) applyDefaults() {
	if c.Finality.BlockTag == "" {
		c.Finality.BlockTag = DefaultBlockTag
	}
	if c.Finality.PollInterval <= 0 {
		c.Finality.PollInterval = DefaultPollInterval
	}
	if c.Finality.Timeout <= 0 {
		c.Finality.Timeout = DefaultFinalityTimeout
	}
	if c.Gas.Strategy == "" {
		c.Gas.Strategy = DefaultGasStrategy
	}
	if c.Gas.Multiplier <= 0 {
		c.Gas.Multiplier = DefaultGasMultiplier
	}
}

// Validate checks the configuration is internally consistent and usable.
func (c *Config) Validate() error {
	if c.Endpoint == "" {
		return errors.New("evm config: endpoint is required")
	}
	if c.ChainID <= 0 {
		return errors.Errorf("evm config: chainID must be positive, got %d", c.ChainID)
	}
	if _, err := c.TokenStateAddress(); err != nil {
		return err
	}
	if c.Contracts.EndorsementVerifier != "" {
		if _, err := client.HexToAddress(c.Contracts.EndorsementVerifier); err != nil {
			return errors.Wrap(err, "evm config: invalid endorsementVerifier address")
		}
	}
	switch c.Finality.BlockTag {
	case client.BlockTagFinalized, client.BlockTagSafe, client.BlockTagLatest:
	default:
		return errors.Errorf("evm config: unsupported finality blockTag [%s]", c.Finality.BlockTag)
	}
	if c.Finality.BlockTag == client.BlockTagFinalized && c.Finality.Timeout < MinFinalizedTagTimeout {
		return errors.Errorf(
			"evm config: finality.timeout [%s] is shorter than the finalized tag's own time-to-finality "+
				"(~%s); it would condemn every transaction regardless of whether it succeeded",
			c.Finality.Timeout, MinFinalizedTagTimeout,
		)
	}
	if err := c.validateGas(); err != nil {
		return err
	}

	return c.validateEndorsement()
}

func (c *Config) validateGas() error {
	switch c.Gas.Strategy {
	case GasStrategyEstimate:
		if c.Gas.Multiplier < 1 {
			return errors.Errorf("evm config: gas multiplier must be at least 1, got %v", c.Gas.Multiplier)
		}
	case GasStrategyFixed:
		if c.Gas.Limit == 0 {
			return errors.New("evm config: gas limit is required when strategy is fixed")
		}
	default:
		return errors.Errorf("evm config: unsupported gas strategy [%s]", c.Gas.Strategy)
	}

	return nil
}

// validateEndorsement enforces the quorum invariants: a positive threshold no larger than the
// endorser set, and every endorser carrying both an address and an FSC identity (one without the
// other cannot be routed to or recovered from, design §6.1). Duplicate addresses are rejected because
// the contract counts distinct signers, so a duplicate would inflate an apparent quorum.
func (c *Config) validateEndorsement() error {
	e := c.Endorsement
	if len(e.Endorsers) == 0 {
		return errors.New("evm config: no endorsers configured")
	}
	if e.Threshold == 0 {
		return errors.New("evm config: endorsement threshold must be positive")
	}
	if int(e.Threshold) > len(e.Endorsers) {
		return errors.Errorf("evm config: endorsement threshold %d exceeds the %d configured endorsers",
			e.Threshold, len(e.Endorsers))
	}

	seen := make(map[client.Address]struct{}, len(e.Endorsers))
	named := make(map[string]struct{}, len(e.Endorsers))
	for i, b := range e.Endorsers {
		if b.FSCIdentity == "" {
			return errors.Errorf("evm config: endorser %d has no fscIdentity", i)
		}
		addr, err := client.HexToAddress(b.Address)
		if err != nil {
			return errors.Wrapf(err, "evm config: endorser %d has an invalid address", i)
		}
		if _, dup := seen[addr]; dup {
			return errors.Errorf("evm config: duplicate endorser address [%s]", b.Address)
		}
		// A repeated fscIdentity inflates the apparent quorum the same way a repeated address does,
		// one level up. An endorser node signs with the one key it is configured with, so two bindings
		// naming it cannot yield two distinct signatures, and the contract counts distinct signers.
		// The set then looks larger than it is: a threshold the endorsers can never reach passes
		// validation here, matches the deployed verifier's address set, and fails at every
		// transaction instead of at startup.
		if _, dup := named[b.FSCIdentity]; dup {
			return errors.Errorf(
				"evm config: endorser identity [%s] is bound twice; one node cannot supply two of the "+
					"distinct signatures a quorum needs", b.FSCIdentity)
		}
		seen[addr] = struct{}{}
		named[b.FSCIdentity] = struct{}{}
	}

	if c.Endorser.Enabled {
		if c.Endorser.Address == "" {
			return errors.New("evm config: endorser.address is required when endorser.enabled is set")
		}
		if _, err := client.HexToAddress(c.Endorser.Address); err != nil {
			return errors.Wrap(err, "evm config: invalid endorser address")
		}
		// The authorizer is deliberately fail-closed: it refuses to build from an empty allowlist
		// rather than default to trusting everyone. Catching that here means a node left this way
		// fails at startup rather than coming up looking healthy and silently never registering as an
		// endorser, which is where this used to surface, as an error log easy to miss during wiring.
		if len(c.Endorsement.Allowlist) == 0 {
			return errors.New("evm config: endorsement.allowlist is required when endorser.enabled is set")
		}
	}

	return nil
}

// TokenStateAddress returns the configured TokenState clone address.
func (c *Config) TokenStateAddress() (client.Address, error) {
	if c.Contracts.TokenState == "" {
		return client.Address{}, errors.New("evm config: contracts.tokenState is required")
	}
	addr, err := client.HexToAddress(c.Contracts.TokenState)
	if err != nil {
		return client.Address{}, errors.Wrap(err, "evm config: invalid tokenState address")
	}

	return addr, nil
}

// IdentityResolver turns the node name a configuration carries into the FSC identity that node
// actually speaks with.
//
// The distinction matters twice over: a session is opened to an identity, not a name, so an
// unresolved name routes nowhere; and the allowlist is compared against the identity a session
// authenticated, so an unresolved name never matches and every request is refused as unauthorized.
type IdentityResolver func(name string) (view.Identity, error)

// EndorserRegistry builds the address to identity registry the endorsement flow routes on, from the
// configured endorser set (design §6.1). Names are resolved through the identity provider, the way
// the fabric endorsement service resolves its own.
func (c *Config) EndorserRegistry(resolve IdentityResolver) (*endorsement.Registry, error) {
	if resolve == nil {
		return nil, errors.New("evm config: no identity resolver for the endorser set")
	}
	endorsers := make([]endorsement.Endorser, 0, len(c.Endorsement.Endorsers))
	for i, b := range c.Endorsement.Endorsers {
		address, err := client.HexToAddress(b.Address)
		if err != nil {
			return nil, errors.Wrapf(err, "evm config: endorser %d has an invalid address", i)
		}
		identity, err := resolve(b.FSCIdentity)
		if err != nil {
			return nil, errors.Wrapf(err, "evm config: cannot resolve the identity of endorser [%s]", b.FSCIdentity)
		}
		if identity.IsNone() {
			return nil, errors.Errorf("evm config: endorser [%s] has no known identity", b.FSCIdentity)
		}
		endorsers = append(endorsers, endorsement.Endorser{Identity: identity, Address: address})
	}

	return endorsement.NewRegistry(endorsers)
}

// EndorserSigner loads this node's endorsement signing key, or nil when the node does not endorse.
func (c *Config) EndorserSigner() (*eip712.Signer, error) {
	if !c.Endorser.Enabled {
		return nil, nil
	}
	key, err := LoadKeyForAddress(c.Endorser.Keystore, c.Endorser.Address)
	if err != nil {
		return nil, err
	}

	return eip712.NewSigner(key), nil
}

// AllowedRequesters returns the identities permitted to request an endorsement. The allowlist is
// compared against the identity a session authenticated, so the configured names have to be resolved
// to identities or nothing ever matches.
func (c *Config) AllowedRequesters(resolve IdentityResolver) ([]view.Identity, error) {
	if resolve == nil {
		return nil, errors.New("evm config: no identity resolver for the allowlist")
	}
	out := make([]view.Identity, 0, len(c.Endorsement.Allowlist))
	for _, name := range c.Endorsement.Allowlist {
		// A name this node cannot resolve is dropped rather than fatal. It could never match an
		// authenticated caller anyway, and failing here would take the endorser down for every other
		// requester too. It is the common case, not the exotic one: the allowlist names every node in
		// the network, and a node does not resolve its own name.
		identity, err := resolve(name)
		if err != nil || identity.IsNone() {
			logger.Debugf("allowlisted requester [%s] has no known identity here and will not be admitted", name)

			continue
		}
		out = append(out, identity)
	}

	return out, nil
}

// ChainIDBig returns the chain id as a big.Int, the form the EIP-712 domain and transaction signing
// need.
func (c *Config) ChainIDBig() *big.Int { return big.NewInt(c.ChainID) }
