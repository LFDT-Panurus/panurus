/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package network

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	ftsconfig "github.com/LFDT-Panurus/panurus/token/services/config"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/services/network/driver"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/utils/lazy"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// ValidationCode represents the status of a transaction on the ledger.
type ValidationCode = driver.ValidationCode

const (
	Valid   = driver.Valid   // Valid indicates the transaction is valid and committed.
	Invalid = driver.Invalid // Invalid indicates the transaction is invalid and has been discarded.
	Busy    = driver.Busy    // Busy indicates the transaction is still being processed.
	Unknown = driver.Unknown // Unknown indicates the transaction state is not known.
)

var logger = logging.MustGetLogger()

// FinalityListener defines the interface for receiving notifications when a transaction's status changes on the ledger.
type FinalityListener interface {
	// OnStatus is called when the status of a transaction changes.
	OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte)
	// OnError is called when the finality event cannot be delivered after all retries are exhausted
	OnError(ctx context.Context, txID string, err error)
}

// GetFunc is a function type that returns identity and raw byte information.
type GetFunc func() (view.Identity, []byte, error)

// TxID represents a unique transaction identifier composed of a nonce and the creator's identity.
type TxID struct {
	// Nonce is a random byte slice used to ensure uniqueness.
	Nonce []byte
	// Creator is the serialized identity of the transaction creator.
	Creator []byte
}

// String returns a string representation of the transaction ID.
func (t *TxID) String() string {
	return fmt.Sprintf("[%s:%s]", base64.StdEncoding.EncodeToString(t.Nonce), base64.StdEncoding.EncodeToString(t.Creator))
}

// TransientMap models the transient data passed with a transaction proposal, which is not persisted on the ledger.
type TransientMap map[string][]byte

// Set adds a raw byte slice to the transient map.
func (m TransientMap) Set(key string, raw []byte) error {
	m[key] = raw

	return nil
}

// Get retrieves a raw byte slice from the transient map.
func (m TransientMap) Get(id string) []byte {
	return m[id]
}

// IsEmpty returns true if the transient map has no entries.
func (m TransientMap) IsEmpty() bool {
	return len(m) == 0
}

// Exists checks if a specific key is present in the transient map.
func (m TransientMap) Exists(key string) bool {
	_, ok := m[key]

	return ok
}

// SetState marshals a Go object into JSON and stores it in the transient map.
func (m TransientMap) SetState(key string, state any) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	m[key] = raw

	return nil
}

// GetState unmarshals a JSON-encoded value from the transient map into a Go object.
func (m TransientMap) GetState(key string, state any) error {
	value, ok := m[key]
	if !ok {
		return errors.Errorf("transient map key [%s] does not exists", key)
	}
	if len(value) == 0 {
		return errors.Errorf("transient map key [%s] is empty", key)
	}

	return json.Unmarshal(value, state)
}

// Envelope wraps a ledger-specific transaction envelope.
type Envelope struct {
	e driver.Envelope
}

// Bytes serializes the envelope into bytes.
func (e *Envelope) Bytes() ([]byte, error) {
	return e.e.Bytes()
}

// FromBytes deserializes an envelope from bytes.
func (e *Envelope) FromBytes(raw []byte) error {
	return e.e.FromBytes(raw)
}

// TxID returns the transaction identifier within the envelope.
func (e *Envelope) TxID() string {
	return e.e.TxID()
}

// MarshalJSON provides custom JSON marshaling for the envelope.
func (e *Envelope) MarshalJSON() ([]byte, error) {
	raw, err := e.e.Bytes()
	if err != nil {
		return nil, err
	}

	return json.Marshal(raw)
}

// UnmarshalJSON provides custom JSON unmarshaling for the envelope.
func (e *Envelope) UnmarshalJSON(raw []byte) error {
	var r []byte
	err := json.Unmarshal(raw, &r)
	if err != nil {
		return err
	}

	return e.e.FromBytes(r)
}

// String returns a string representation of the envelope.
func (e *Envelope) String() string {
	return e.e.String()
}

// LocalMembership provides access to identities managed by the local node.
type LocalMembership struct {
	lm driver.LocalMembership
}

// NewLocalMembership creates a new LocalMembership wrapper.
func NewLocalMembership(lm driver.LocalMembership) *LocalMembership {
	return &LocalMembership{lm: lm}
}

// DefaultIdentity returns the default identity of the local node.
func (l *LocalMembership) DefaultIdentity() view.Identity {
	return l.lm.DefaultIdentity()
}

// AnonymousIdentity returns a fresh anonymous identity for privacy-preserving operations.
func (l *LocalMembership) AnonymousIdentity() (view.Identity, error) {
	return l.lm.AnonymousIdentity()
}

// Ledger provides high-level access to the distributed ledger for status checks and state queries.
type Ledger struct {
	l driver.Ledger
}

// Status returns the validation code for a specific transaction ID.
func (l *Ledger) Status(id string) (ValidationCode, string, error) {
	vc, err := l.l.Status(id)
	if err != nil {
		return 0, "", err
	}

	return vc, "", nil
}

// GetStates returns the raw byte values for the given keys in a specific namespace.
func (l *Ledger) GetStates(ctx context.Context, namespace string, keys ...string) ([][]byte, error) {
	return l.l.GetStates(ctx, namespace, keys...)
}

// TransferMetadataKey returns the ledger key associated with transfer metadata for a given key.
func (l *Ledger) TransferMetadataKey(k string) (string, error) {
	return l.l.TransferMetadataKey(k)
}

// Network serves as the primary bridge to a specific blockchain network (e.g., Fabric or FabricX).
// it provides methods for broadcasting transactions, requesting approvals, and monitoring finality.
type Network struct {
	n               driver.Network
	localMembership *LocalMembership
}

// NewNetwork creates a new Network instance.
func NewNetwork(n driver.Network, localMembership *LocalMembership) *Network {
	return &Network{n: n, localMembership: localMembership}
}

// Name returns the identifier of the network.
func (n *Network) Name() string {
	return n.n.Name()
}

// Channel returns the name of the channel or partition within the network.
func (n *Network) Channel() string {
	return n.n.Channel()
}

// Broadcast submits a transaction envelope or generic blob to the network's ordering service.
func (n *Network) Broadcast(ctx context.Context, blob any) error {
	switch b := blob.(type) {
	case *Envelope:
		return n.n.Broadcast(ctx, b.e)
	default:
		return n.n.Broadcast(ctx, b)
	}
}

// AnonymousIdentity returns a fresh anonymous identity from the local membership.
func (n *Network) AnonymousIdentity() (view.Identity, error) {
	return n.n.LocalMembership().AnonymousIdentity()
}

// NewEnvelope creates a new, empty ledger-specific transaction envelope.
func (n *Network) NewEnvelope() *Envelope {
	return &Envelope{e: n.n.NewEnvelope()}
}

// RequestApproval sends a token request to an endorsement service and returns the resulting endorsed envelope.
// metadata carries optional application-level key-value pairs forwarded to the approver backend.
func (n *Network) RequestApproval(context view.Context, tms *token.ManagementService, requestRaw []byte, signer view.Identity, txID TxID, metadata driver.TransientMap) (*Envelope, error) {
	env, err := n.n.RequestApproval(context, tms, requestRaw, signer, driver.TxID{
		Nonce:   txID.Nonce,
		Creator: txID.Creator,
	}, metadata)
	if err != nil {
		return nil, err
	}

	return &Envelope{e: env}, nil
}

// ComputeTxID calculates the transaction identifier in the network's native format.
func (n *Network) ComputeTxID(id *TxID) string {
	temp := &driver.TxID{
		Nonce:   id.Nonce,
		Creator: id.Creator,
	}
	txID := n.n.ComputeTxID(temp)
	id.Nonce = temp.Nonce
	id.Creator = temp.Creator

	return txID
}

// FetchPublicParameters retrieves the public parameters for a specific namespace from the network.
func (n *Network) FetchPublicParameters(namespace string) ([]byte, error) {
	return n.n.FetchPublicParameters(namespace)
}

// QueryTokens retrieves the raw byte representation of tokens from the ledger.
func (n *Network) QueryTokens(ctx context.Context, namespace string, IDs []*token2.ID) ([][]byte, error) {
	return n.n.QueryTokens(ctx, namespace, IDs)
}

// AreTokensSpent checks the spent status of multiple tokens on the ledger.
func (n *Network) AreTokensSpent(ctx context.Context, namespace string, tokenIDs []*token2.ID, meta []string) ([]bool, error) {
	return n.n.AreTokensSpent(ctx, namespace, tokenIDs, meta)
}

// LocalMembership returns the local membership service associated with this network.
func (n *Network) LocalMembership() *LocalMembership {
	return n.localMembership
}

// AddFinalityListener registers a listener to be notified when a transaction reaches a final state on the ledger.
func (n *Network) AddFinalityListener(namespace string, txID string, listener FinalityListener) error {
	return n.n.AddFinalityListener(namespace, txID, listener)
}

// GetTransactionStatus retrieves the current status and token request hash for a transaction.
func (n *Network) GetTransactionStatus(ctx context.Context, namespace, txID string) (status int, tokenRequestHash []byte, message string, err error) {
	return n.n.GetTransactionStatus(ctx, namespace, txID)
}

// LookupTransferMetadataKey performs a ledger scan to find a metadata key matching the provided sub-key.
func (n *Network) LookupTransferMetadataKey(namespace, key string, timeout time.Duration, opts ...token.ServiceOption) ([]byte, error) {
	return n.n.LookupTransferMetadataKey(namespace, key, timeout)
}

// Ledger provides access to the ledger service for this network.
func (n *Network) Ledger() (*Ledger, error) {
	l, err := n.n.Ledger()
	if err != nil {
		return nil, err
	}

	return &Ledger{l: l}, nil
}

// Normalize fills in default values for network, channel, and namespace in the service options.
func (n *Network) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	return n.n.Normalize(opt)
}

// Connect establishes a connection to the network for a specific namespace.
func (n *Network) Connect(ns string) ([]token.ServiceOption, error) {
	return n.n.Connect(ns)
}

// Provider manages multiple Network instances, providing lazy initialization and lookup by network/channel ID.
type Provider struct {
	networks        lazy.Provider[netId, *Network]
	networkProvider *networkProvider
	configService   *ftsconfig.Service
}

type netId struct {
	network, channel string
}

func key(id netId) string {
	return id.network + id.channel
}

// NewProvider returns a new network Provider instance.
func NewProvider(configService *ftsconfig.Service) *Provider {
	ms := &networkProvider{drivers: make([]driver.Driver, 0)}

	return &Provider{
		networkProvider: ms,
		networks:        lazy.NewProviderWithKeyMapper(key, ms.newNetwork),
		configService:   configService,
	}
}

// RegisterDriver adds a new network driver to the provider.
func (p *Provider) RegisterDriver(driver driver.Driver) {
	p.networkProvider.registerDriver(driver)
}

// GetNetwork returns the Network instance for the specified network and channel identifiers.
func (p *Provider) GetNetwork(network string, channel string) (*Network, error) {
	logger.Debugf("GetNetwork: [%s:%s]", network, channel)

	return p.networks.Get(netId{network: network, channel: channel})
}

// Normalize ensures that network-related options are fully populated based on the configured environment.
func (p *Provider) Normalize(opt *token.ServiceOptions) (*token.ServiceOptions, error) {
	if opt == nil {
		return nil, errors.New("no service options provided")
	}
	n, err := p.GetNetwork(opt.Network, opt.Channel)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get network [%s:%s]", opt.Network, opt.Channel)
	}

	return n.Normalize(opt)
}

// Connect initializes and connects all configured token management systems to their respective networks.
func (p *Provider) Connect() error {
	configurations, err := p.configService.Configurations()
	if err != nil {
		return err
	}
	for _, tmsConfig := range configurations {
		tmsID := tmsConfig.ID()
		logger.Infof("start token management service [%s]...", tmsID)

		// connect network
		net, err := p.GetNetwork(tmsID.Network, tmsID.Channel)
		if err != nil {
			return errors.Wrapf(err, "failed to get network [%s]", tmsID)
		}
		_, err = net.Connect(tmsID.Namespace)
		if err != nil {
			return errors.WithMessagef(err, "failed to connect to connect backend to tms [%s]", tmsID)
		}
	}

	return nil
}

// networkProvider instantiates new networks based on the registered drivers
type networkProvider struct {
	drivers []driver.Driver
}

func (np *networkProvider) registerDriver(driver driver.Driver) {
	np.drivers = append(np.drivers, driver)
}

func (np *networkProvider) newNetwork(netId netId) (*Network, error) {
	network, channel := netId.network, netId.channel
	var errs []error
	for _, d := range np.drivers {
		logger.Debugf("new network service for [%s:%s]", network, channel)
		nw, err := d.New(network, channel)
		if err != nil {
			errs = append(errs, errors.WithMessagef(err, "failed to create network [%s:%s]", network, channel))

			continue
		}
		logger.Debugf("new network [%s:%s]", network, channel)

		return &Network{
			n:               nw,
			localMembership: &LocalMembership{lm: nw.LocalMembership()},
		}, nil
	}

	return nil, errors.Errorf("no network driver found for [%s:%s], errs [%v]", network, channel, errs)
}

// GetInstance returns a network instance for the given network and channel
func GetInstance(sp token.ServiceProvider, network, channel string) *Network {
	n, err := GetProvider(sp).GetNetwork(network, channel)
	if err != nil {
		logger.Errorf("Failed to get network [%s:%s]: %s", network, channel, err)

		return nil
	}

	return n
}

// GetProvider retrieves the network Provider from the service provider.
func GetProvider(sp token.ServiceProvider) *Provider {
	s, err := sp.GetService(&Provider{})
	if err != nil {
		panic(fmt.Sprintf("Failed to get service: %s", err))
	}

	return s.(*Provider)
}
