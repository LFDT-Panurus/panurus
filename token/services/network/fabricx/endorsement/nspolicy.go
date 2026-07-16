/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package endorsement

import (
	"context"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/network/fabric/endorsement/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
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

// SelectEndorsers returns a random subset of configured that satisfies the namespace's
// endorsement policy, as reported by the FabricX query service.
func (s *QueryServiceEndorserSelector) SelectEndorsers(ctx context.Context, tmsID token2.TMSID, configured []view.Identity) ([]view.Identity, error) {
	candidates, err := s.candidateMSPSets(ctx, tmsID)
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

// candidateMSPSets fetches the namespace policy for tmsID.Namespace and returns the
// list of MSP-ID sets, any one of which jointly satisfies the policy.
func (s *QueryServiceEndorserSelector) candidateMSPSets(ctx context.Context, tmsID token2.TMSID) ([][]string, error) {
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

	return candidateMSPSetsFromPolicies(policies, tmsID.Namespace)
}

// candidateMSPSetsFromPolicies extracts, from an already-fetched NamespacePolicies
// response, the list of MSP-ID sets any one of which jointly satisfies the endorsement
// policy of namespace. It is pure and does not perform any I/O, so it is unit-testable
// without a gRPC client.
func candidateMSPSetsFromPolicies(policies *applicationpb.NamespacePolicies, namespace string) ([][]string, error) {
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
