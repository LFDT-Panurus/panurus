/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package interactive

import (
	"fmt"
	"sync"
	"time"

	token2 "github.com/LFDT-Panurus/panurus/token"
	"github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/services/metrics"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

//go:generate counterfeiter -o mock/backend.go -fake-name BackendMock . Backend
type Backend interface {
	Load(context view.Context, cr *CertificationRequest) ([][]byte, error)
}

//go:generate counterfeiter -o mock/responder_registry.go -fake-name ResponderRegistryMock . ResponderRegistry
type ResponderRegistry interface {
	RegisterResponder(responder view.View, initiatedBy any) error
}

type CertificationService struct {
	ResponderRegistry ResponderRegistry

	startOnce sync.Once
	mu        sync.RWMutex
	wallets   map[string]string
	backend   Backend
	metrics   *Metrics
}

func NewCertificationService(responderRegistry ResponderRegistry, mp metrics.Provider, backend Backend) *CertificationService {
	return &CertificationService{
		wallets:           map[string]string{},
		metrics:           NewMetrics(mp),
		backend:           backend,
		ResponderRegistry: responderRegistry,
	}
}

// Start registers the certification responder exactly once. It returns an error
// if the underlying registry call fails.
func (c *CertificationService) Start() error {
	var startErr error

	c.startOnce.Do(func() {
		startErr = c.ResponderRegistry.RegisterResponder(c, &CertificationRequestView{})
	})

	return startErr
}

func (c *CertificationService) SetWallet(tms *token2.ManagementService, wallet string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wallets[tms.Network()+":"+tms.Channel()+":"+tms.Namespace()] = wallet
}

func (c *CertificationService) Call(context view.Context) (any, error) {
	// 1. receive request
	logger.Debugf("receive certification request [%s]", context.ID())
	s := session.JSON(context)

	var cr *CertificationRequest
	if err := s.Receive(&cr); err != nil {
		return nil, errors.WithMessagef(err, "failed receiving certification request")
	}

	if cr == nil {
		return nil, errors.Errorf("received nil certification request")
	}

	// 2. validate request
	if cr.Channel == "" || cr.Namespace == "" {
		return nil, errors.Errorf("invalid certification request: channel and namespace must not be empty [%s]", cr)
	}

	if len(cr.IDs) == 0 {
		return nil, errors.Errorf("invalid certification request: no token IDs provided [%s]", cr)
	}

	if len(cr.IDs) > MaxTokensPerRequest {
		return nil, errors.Errorf("invalid certification request: too many token IDs (%d > %d) [%s]", len(cr.IDs), MaxTokensPerRequest, cr)
	}

	if len(cr.Request) > MaxRequestBytes {
		return nil, errors.Errorf("invalid certification request: request payload too large (%d > %d bytes) [%s]", len(cr.Request), MaxRequestBytes, cr)
	}

	logger.Debugf("received certification request [%v]", cr)

	// 3. load token outputs
	tokenOutputs, err := c.backend.Load(context, cr)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tokens [%s:%s][%v]", cr.Channel, cr.Namespace, cr.IDs)
	}

	if len(tokenOutputs) != len(cr.IDs) {
		return nil, errors.Errorf(
			"token output count mismatch: backend returned %d outputs for %d IDs [%s]",
			len(tokenOutputs), len(cr.IDs), cr,
		)
	}

	// 4. certify token output
	logger.Debugf("certify commitments for [%v]...", cr.IDs)
	tms, err := token2.GetManagementService(
		context,
		token2.WithNetwork(cr.Network),
		token2.WithChannel(cr.Channel),
		token2.WithNamespace(cr.Namespace),
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get tms for [%s:%s:%s]", cr.Network, cr.Channel, cr.Namespace)
	}

	walletKey := tms.Network() + ":" + tms.Channel() + ":" + tms.Namespace()
	logger.Debugf("lookup wallet ID with key [%s]", walletKey)

	c.mu.RLock()
	walletID, ok := c.wallets[walletKey]
	c.mu.RUnlock()

	if !ok {
		logger.Errorf("failed getting certifier wallet, namespace not registered [%s]", cr)

		return nil, errors.Errorf("failed getting certifier wallet, namespace not registered [%s]", cr)
	}

	logger.Debugf("certify with wallet [%s]", walletID)

	w, err := tms.WalletManager().CertifierWallet(context.Context(), walletID)
	if err != nil {
		return nil, errors.Errorf("failed getting certifier wallet, wallet [%s] not found [%s:%s][%v]", walletID, cr.Channel, cr.Namespace, cr.IDs)
	}

	logger.Debugf("certify request [%v]", cr)

	certifications, err := tms.CertificationManager().Certify(w, cr.IDs, tokenOutputs, cr.Request)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed certifying tokens [%s:%s][%v]", cr.Channel, cr.Namespace, cr.IDs)
	}

	// 5. respond
	logger.Debugf("send back certifications for [%v]", cr.IDs)

	if err := s.Send(certifications); err != nil {
		return nil, errors.WithMessagef(err, "failed sending certifications")
	}

	labels := []string{
		"network", cr.Network,
		"channel", cr.Channel,
		"namespace", cr.Namespace,
	}
	c.metrics.CertifiedTokens.With(labels...).Add(float64(len(cr.IDs)))

	return nil, nil
}

type CertificationRequest struct {
	Network, Channel, Namespace string
	IDs                         []*token.ID
	Request                     []byte
}

func (cr *CertificationRequest) String() string {
	return fmt.Sprintf("CertificationRequest[%s:%s:%s][ids=%d,req=%d bytes]", cr.Network, cr.Channel, cr.Namespace, len(cr.IDs), len(cr.Request))
}

type CertificationRequestView struct {
	network, channel, ns string
	ids                  []*token.ID
	certifier            view.Identity
	responseTimeout      time.Duration
}

func NewCertificationRequestView(network, channel, ns string, certifier view.Identity, ids ...*token.ID) *CertificationRequestView {
	return &CertificationRequestView{
		network:         network,
		channel:         channel,
		certifier:       certifier,
		ns:              ns,
		ids:             ids,
		responseTimeout: DefaultResponseTimeout,
	}
}

func (i *CertificationRequestView) Call(context view.Context) (any, error) {
	if len(i.ids) == 0 {
		return nil, errors.Errorf("certification request has no token IDs")
	}

	if i.certifier.IsNone() {
		return nil, errors.Errorf("no certifiers defined")
	}

	// 1. prepare request
	logger.Debugf("prepare certification request for [%v]", i.ids)
	tms, err := token2.GetManagementService(
		context,
		token2.WithNetwork(i.network),
		token2.WithChannel(i.channel),
		token2.WithNamespace(i.ns),
	)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed getting tms for [%s:%s:%s]", i.network, i.channel, i.ns)
	}

	cm := tms.CertificationManager()

	cr, err := cm.NewCertificationRequest(i.ids)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed creating certification request for [%v]", i.ids)
	}

	// 2. send request
	logger.Debugf("send certification request for [%v]", i.ids)

	s, err := session.NewJSON(context, i, i.certifier)
	if err != nil {
		return nil, errors.WithMessagef(err, "failed opening session to [%s]", i.certifier)
	}

	if err := s.Send(&CertificationRequest{
		Network:   i.network,
		Channel:   i.channel,
		Namespace: i.ns,
		IDs:       i.ids,
		Request:   cr,
	}); err != nil {
		return nil, errors.WithMessagef(err, "failed sending certification request [%v] to [%s]", i.ids, i.certifier)
	}

	// 3. wait response
	logger.Debugf("wait certification request response for [%v]", i.ids)

	var certifications [][]byte
	if err := s.ReceiveWithTimeout(&certifications, i.responseTimeout); err != nil {
		return nil, errors.WithMessagef(err, "failed receiving certifications [%v] from [%s]", i.ids, i.certifier)
	}

	// 4. Validate response
	logger.Debugf("validate certification request response for [%v]", i.ids)

	if len(certifications) != len(i.ids) {
		return nil, errors.Errorf(
			"certifier returned %d certifications for %d token IDs [%v] from [%s]",
			len(certifications), len(i.ids), i.ids, i.certifier,
		)
	}

	processedCertifications, err := cm.VerifyCertifications(i.ids, certifications)
	if err != nil {
		logger.Errorf("failed verifying certifications of [%v] from [%s] with err [%s]", i.ids, i.certifier, err)

		return nil, errors.WithMessagef(err, "failed verifying certifications of [%v] from [%s]", i.ids, i.certifier)
	}

	if len(processedCertifications) != len(i.ids) {
		return nil, errors.Errorf(
			"certification manager returned %d processed certifications for %d token IDs from [%s]",
			len(processedCertifications), len(i.ids), i.certifier,
		)
	}

	logger.Debugf("certifications of [%v] from [%s] are valid", i.ids, i.certifier)

	// 5. return token certifications in the form of a map
	result := map[*token.ID][]byte{}
	for index, id := range i.ids {
		result[id] = processedCertifications[index]
	}

	return result, nil
}
