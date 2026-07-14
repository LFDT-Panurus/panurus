/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fsc

import (
	"context"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabric/services/endorser"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

//go:generate counterfeiter -o mock/fabric_envelope.go -fake-name Envelope . Envelope
type Envelope = driver.Envelope

//go:generate counterfeiter -o mock/fabric_rws.go -fake-name FabricRWSet . FabricRWSet
type FabricRWSet = driver.RWSet

//go:generate counterfeiter -o mock/tmsp.go -fake-name TokenManagementSystemProvider . TokenManagementSystemProvider
type TokenManagementSystemProvider interface {
	GetManagementService(opts ...token.ServiceOption) (*token.ManagementService, error)
}

//go:generate counterfeiter -o mock/translator.go -fake-name Translator . Translator
type Translator interface {
	AddPublicParamsDependency() error
	CommitTokenRequest(raw []byte, storeHash bool) ([]byte, error)
	Write(ctx context.Context, action any) error
}

type TranslatorProviderFunc = func(txID string, namespace string, rws *fabric.RWSet) (Translator, error)

// EndorserService defines the behaviors of the FSC's fabric endorser service that are needed by this package
//
//go:generate counterfeiter -o mock/endorser_service.go -fake-name EndorserService . EndorserService
type EndorserService interface {
	NewTransaction(context view.Context, opts ...fabric.TransactionOption) (*endorser.Transaction, error)
	ReceiveTx(ctx view.Context) (*endorser.Transaction, error)
	CollectEndorsements(ctx view.Context, tx *endorser.Transaction, timeOut time.Duration, parties ...view.Identity) error
	Endorse(ctx view.Context, tx *endorser.Transaction, identities ...view.Identity) (any, error)
	EndorserID(tmsID token.TMSID) (view.Identity, error)
}

// Context is an alias for view.Context
//
//go:generate counterfeiter -o mock/ctx.go -fake-name Context . Context
type Context = view.Context

// FabricTransaction is an alias for driver.Transaction
//
//go:generate counterfeiter -o mock/fabric_transaction.go -fake-name FabricTransaction . FabricTransaction
type FabricTransaction = driver.Transaction

type IdentityProvider interface {
	Identity(string) view.Identity
}

type ViewManager interface {
	InitiateView(ctx context.Context, view view.View) (any, error)
}

type ViewRegistry interface {
	RegisterResponder(responder view.View, initiatedBy any) error
}

// NamespaceTxProcessor models a namespace transaction processor.
type NamespaceTxProcessor interface {
	// EnableTxProcessing signals the backend to process all the transactions for the given tms id
	EnableTxProcessing(tmsID token.TMSID) error
}

// MSPManager provides MSP-based identity validation and signature verification for a Fabric channel.
// It is used to verify that the proposal creator is known to the network.
//
//go:generate counterfeiter -o mock/msp_manager.go -fake-name MSPManager . MSPManager
type MSPManager interface {
	// IsValid checks that the identity is valid (i.e., known to the network via MSP)
	IsValid(identity view.Identity) error
	// GetVerifier returns a verifier for the given identity, which can be used to verify signatures
	GetVerifier(identity view.Identity) (driver.Verifier, error)
}

// ACLProvider offers ACL-related services
//
//go:generate counterfeiter -o mock/acl_provider.go -fake-name ACLProvider . ACLProvider
type ACLProvider interface {
	// CheckACL checks the ACL for the resource for the Channel using the
	// SignedProposal from which an id can be extracted for testing against a policy
	CheckACL(signedProp *fabric.SignedProposal) error
}

// ChannelProvider provides access to the MSP manager for a given Fabric network and channel.
//
//go:generate counterfeiter -o mock/channel_provider.go -fake-name ChannelProvider . ChannelProvider
type ChannelProvider interface {
	// GetMSPManager returns the MSP manager for the given network and channel
	GetMSPManager(network, channel string) (MSPManager, error)
	GetACLProvider(network, channel string) (ACLProvider, error)
}
