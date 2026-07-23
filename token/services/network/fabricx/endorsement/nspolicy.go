/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"crypto/ecdsa"
	"strings"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	mspx509 "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/core/generic/msp/x509"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/committer/queryservice"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/hyperledger/fabric-protos-go-apiv2/common"
	"github.com/hyperledger/fabric-protos-go-apiv2/msp"
	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/hyperledger/fabric-x-common/common/policies/inquire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ecdsaScheme is the only applicationpb.ThresholdRule.Scheme value currently supported for
// endorser selection, since FSC endorser identities are X.509/ECDSA based.
const ecdsaScheme = "ECDSA"

// requestTimeout bounds the GetNamespacePolicies RPC call.
const requestTimeout = 30 * time.Second

// ChannelMSPManager provides MSP identifier resolution for a Fabric(x) channel.
type ChannelMSPManager interface {
	GetMSPIdentifier(sid []byte) (string, error)
}

// ChannelMSPManagerProvider resolves the ChannelMSPManager for a given network/channel.
type ChannelMSPManagerProvider interface {
	GetMSPManager(network, channel string) (fsc.MSPManager, error)
}

// QueryServiceEndorserSelector selects, among the configured FSC endorsers, a random
// subset that satisfies the real endorsement policy of the target namespace, fetched
// from the FabricX query service's GetNamespacePolicies RPC.
type QueryServiceEndorserSelector struct {
	grpcClientProvider queryservice.GRPCClientProvider
	channelProvider    ChannelMSPManagerProvider
}

// NewQueryServiceEndorserSelector returns a new QueryServiceEndorserSelector.
func NewQueryServiceEndorserSelector(grpcClientProvider queryservice.GRPCClientProvider, channelProvider ChannelMSPManagerProvider) *QueryServiceEndorserSelector {
	return &QueryServiceEndorserSelector{grpcClientProvider: grpcClientProvider, channelProvider: channelProvider}
}

// SelectEndorsers returns the endorser(s), among configured, that satisfy the namespace's
// endorsement policy, as reported by the FabricX query service. For an MSP-rule policy this
// is a random policy-satisfying subset; for a threshold-rule policy this is the single
// configured endorser whose identity carries the policy's public key.
func (s *QueryServiceEndorserSelector) SelectEndorsers(ctx context.Context, tmsID token2.TMSID, configured []view.Identity) ([]view.Identity, error) {
	nsPolicy, err := s.namespacePolicy(ctx, tmsID)
	if err != nil {
		return nil, err
	}

	if rule := nsPolicy.GetThresholdRule(); rule != nil {
		endorser, err := endorserForThresholdRule(rule, configured)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed selecting endorser for namespace [%s]", tmsID.Namespace)
		}

		return []view.Identity{endorser}, nil
	}

	candidates, err := candidateMSPSetsFromNamespacePolicy(nsPolicy, tmsID.Namespace)
	if err != nil {
		return nil, err
	}

	mspManager, err := s.channelProvider.GetMSPManager(tmsID.Network, tmsID.Channel)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get msp manager for [%s:%s]", tmsID.Network, tmsID.Channel)
	}
	mspOf := func(id view.Identity) (string, error) { return mspManager.GetMSPIdentifier(id) }

	return fsc.SelectEndorsersForMSPSets(configured, mspOf, candidates)
}

// namespacePolicy fetches, via the FabricX query service's GetNamespacePolicies RPC, the
// endorsement policy in effect for tmsID.Namespace.
func (s *QueryServiceEndorserSelector) namespacePolicy(ctx context.Context, tmsID token2.TMSID) (*applicationpb.NamespacePolicy, error) {
	cc, err := s.grpcClientProvider.QueryServiceClient(tmsID.Network)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to get query service client for [%s]", tmsID.Network)
	}
	client := committerpb.NewQueryServiceClient(cc)

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	policies, err := client.GetNamespacePolicies(reqCtx, &emptypb.Empty{})
	if err != nil {
		return nil, errors.WithMessagef(err, "failed to fetch namespace policies for [%s]", tmsID.Network)
	}

	return namespacePolicyFor(policies, tmsID.Namespace)
}

// namespacePolicyFor extracts and unmarshals, from an already-fetched NamespacePolicies
// response, the endorsement policy of namespace. It is pure and does not perform any I/O,
// so it is unit-testable without a gRPC client.
func namespacePolicyFor(policies *applicationpb.NamespacePolicies, namespace string) (*applicationpb.NamespacePolicy, error) {
	var item *applicationpb.PolicyItem
	for _, p := range policies.GetPolicies() {
		if p.GetNamespace() == namespace {
			item = p

			break
		}
	}
	if item == nil {
		return nil, errors.Errorf("no endorsement policy found for namespace [%s]", namespace)
	}

	nsPolicy := &applicationpb.NamespacePolicy{}
	if err := proto.Unmarshal(item.GetPolicy(), nsPolicy); err != nil {
		return nil, errors.WithMessagef(err, "failed to unmarshal namespace policy for [%s]", namespace)
	}

	return nsPolicy, nil
}

// candidateMSPSetsFromNamespacePolicy returns the list of MSP-ID sets any one of which
// jointly satisfies the MSP-rule endorsement policy nsPolicy. It errors for a
// threshold-rule policy.
func candidateMSPSetsFromNamespacePolicy(nsPolicy *applicationpb.NamespacePolicy, namespace string) ([][]string, error) {
	if nsPolicy.GetThresholdRule() != nil {
		return nil, errors.Errorf("namespace [%s] uses a threshold-rule endorsement policy, which is not identity/MSP based and cannot be mapped to a subset of endorsers", namespace)
	}
	mspRule := nsPolicy.GetMspRule()
	if len(mspRule) == 0 {
		return nil, errors.Errorf("namespace [%s] has no supported endorsement policy rule", namespace)
	}

	sigPolicyEnvelope := &common.SignaturePolicyEnvelope{}
	if err := proto.Unmarshal(mspRule, sigPolicyEnvelope); err != nil {
		return nil, errors.WithMessagef(err, "failed to unmarshal msp-rule signature policy for [%s]", namespace)
	}

	principalSets := inquire.NewInquireableSignaturePolicy(sigPolicyEnvelope).SatisfiedBy()
	if len(principalSets) == 0 {
		return nil, errors.Errorf("namespace [%s] endorsement policy cannot be satisfied by any principal set", namespace)
	}

	candidates := make([][]string, 0, len(principalSets))
	for _, principalSet := range principalSets {
		mspIDs, err := mspIDsOf(principalSet)
		if err != nil {
			return nil, errors.WithMessagef(err, "failed to interpret endorsement policy for namespace [%s]", namespace)
		}
		candidates = append(candidates, mspIDs)
	}

	return candidates, nil
}

// mspIDsOf extracts the MSP IDs required by a principal set. Only role-based (MEMBER,
// ADMIN, ...) and organization-unit principals are supported, as they map onto MSPs;
// any other principal classification results in an error.
func mspIDsOf(principalSet []*msp.MSPPrincipal) ([]string, error) {
	mspIDs := make([]string, 0, len(principalSet))
	for _, principal := range principalSet {
		switch principal.GetPrincipalClassification() {
		case msp.MSPPrincipal_ROLE:
			role := &msp.MSPRole{}
			if err := proto.Unmarshal(principal.GetPrincipal(), role); err != nil {
				return nil, errors.WithMessagef(err, "failed to unmarshal MSP role principal")
			}
			mspIDs = append(mspIDs, role.GetMspIdentifier())
		case msp.MSPPrincipal_ORGANIZATION_UNIT:
			ou := &msp.OrganizationUnit{}
			if err := proto.Unmarshal(principal.GetPrincipal(), ou); err != nil {
				return nil, errors.WithMessagef(err, "failed to unmarshal organization-unit principal")
			}
			mspIDs = append(mspIDs, ou.GetMspIdentifier())
		default:
			return nil, errors.Errorf("unsupported principal classification [%s] in namespace endorsement policy", principal.GetPrincipalClassification())
		}
	}

	return mspIDs, nil
}

// endorserForThresholdRule returns the single configured endorser whose own MSP/X.509
// identity carries the public key embedded in rule. A ThresholdRule names exactly one
// signer via a raw public key rather than an MSP principal, so there is no candidate MSP
// set to compute; instead, the matching endorser (if any) must be identified directly by
// comparing keys.
//
// Matching is restricted to the ECDSA scheme: it is also the scheme FSC's own MSP identities
// use, and FSC independently re-verifies every endorsement against the endorser's MSP/X.509
// key before broadcast, so a signature can only satisfy both that check and the namespace's
// on-chain ThresholdRule check if the two keys are, in fact, the same key. BLS and EdDSA
// ThresholdRule namespaces cannot be endorsed through the current FSC signing path.
func endorserForThresholdRule(rule *applicationpb.ThresholdRule, configured []view.Identity) (view.Identity, error) {
	if !strings.EqualFold(rule.GetScheme(), ecdsaScheme) {
		return nil, errors.Errorf("threshold-rule endorsement policy uses unsupported scheme [%s]; only [%s] is supported for endorser selection", rule.GetScheme(), ecdsaScheme)
	}

	genericKey, err := mspx509.PemDecodeKey(rule.GetPublicKey())
	if err != nil {
		return nil, errors.WithMessage(err, "failed to parse threshold-rule public key")
	}
	ruleKey, ok := genericKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.Errorf("threshold-rule public key is not an ECDSA key")
	}

	var matches []view.Identity
	for _, id := range configured {
		key, ok := ecdsaPublicKeyOf(id)
		if !ok {
			continue
		}
		if key.Equal(ruleKey) {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 0:
		return nil, errors.Errorf("no configured endorser's identity matches the threshold-rule public key")
	case 1:
		return matches[0], nil
	default:
		return nil, errors.Errorf("[%d] configured endorsers' identities match the threshold-rule public key; expected exactly one", len(matches))
	}
}

// ecdsaPublicKeyOf extracts the ECDSA public key embedded in a configured endorser's
// serialized MSP identity (an msp.SerializedIdentity carrying a PEM-encoded X.509
// certificate). It returns ok=false, without error, for identities that are not
// ECDSA-X.509-shaped, since those simply cannot match a ThresholdRule's ECDSA key.
func ecdsaPublicKeyOf(id view.Identity) (*ecdsa.PublicKey, bool) {
	si := &msp.SerializedIdentity{}
	if err := proto.Unmarshal(id, si); err != nil {
		return nil, false
	}

	genericKey, err := mspx509.PemDecodeKey(si.GetIdBytes())
	if err != nil {
		return nil, false
	}

	key, ok := genericKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, false
	}

	return key, true
}
