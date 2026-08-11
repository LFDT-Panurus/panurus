/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	view2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/view"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

type StreamExternalWalletMsgType = int

const (
	_ StreamExternalWalletMsgType = iota
	SigRequest
	SignResponse
	Done
)

// StreamExternalWalletMsg is the root message that the remote wallet and the ttx package exchange.
type StreamExternalWalletMsg struct {
	// Type is the type of this message
	Type StreamExternalWalletMsgType
	// Raw will be interpreted following Type
	Raw []byte
}

// NewStreamExternalWalletMsg creates a new root message for the given type and value
func NewStreamExternalWalletMsg(Type StreamExternalWalletMsgType, v any) (*StreamExternalWalletMsg, error) {
	var raw []byte
	if v != nil {
		var err error
		raw, err = json.Marshal(v)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to marshal [%v]", v)
		}
	}

	return &StreamExternalWalletMsg{Type: Type, Raw: raw}, nil
}

// StreamExternalWalletSignRequest is a message to request a signature
type StreamExternalWalletSignRequest struct {
	Party   view.Identity
	Message []byte
}

// StreamExternalWalletSignResponse is a message to respond to a request of signature
type StreamExternalWalletSignResponse struct {
	Sigma []byte
}

// StreamExternalWalletSignerServer is the signer server executed by the remote wallet
type StreamExternalWalletSignerServer struct {
	stream view2.Stream
}

func NewStreamExternalWalletSignerServer(stream view2.Stream) *StreamExternalWalletSignerServer {
	return &StreamExternalWalletSignerServer{stream: stream}
}

func (s *StreamExternalWalletSignerServer) Sign(party view.Identity, message []byte) ([]byte, error) {
	logger.Debugf("send sign request for party [%s]", party)
	msg, err := NewStreamExternalWalletMsg(SigRequest, &StreamExternalWalletSignRequest{
		Party:   party,
		Message: message,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal sign request message")
	}
	if err := s.stream.Send(msg); err != nil {
		return nil, err
	}
	logger.Debugf("receive response, party [%s]", party)

	msg = &StreamExternalWalletMsg{}
	if err := s.stream.Recv(msg); err != nil {
		return nil, err
	}
	if msg.Type != SignResponse {
		return nil, errors.Errorf("expected sign response msg, got [%d]", msg.Type)
	}
	response := &StreamExternalWalletSignResponse{}
	if err := json.Unmarshal(msg.Raw, response); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal sign response")
	}

	return response.Sigma, nil
}

func (s *StreamExternalWalletSignerServer) Done() error {
	logger.Debug("send done...")
	msg, err := NewStreamExternalWalletMsg(Done, nil)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal sign request message")
	}
	if err := s.stream.Send(msg); err != nil {
		return err
	}

	return nil
}

type SignerProvider interface {
	GetSigner(party view.Identity) (token.Signer, error)
}

// StreamExternalWalletSignerClient is the signer client executed where Panurus is in execution
type StreamExternalWalletSignerClient struct {
	sp      SignerProvider
	stream  view2.Stream
	timeout time.Duration
	// input carries the signature requests decoded by init. It is closed when the remote
	// wallet signals that no more signatures are required.
	input chan *StreamExternalWalletSignRequest
	// err carries the first failure hit by init. It is buffered so that init can always
	// complete its send and return, even if nobody is receiving anymore.
	err chan error
	// done is closed once Respond stops consuming input, so that init never blocks forever
	// handing over a request that nobody will take.
	done     chan struct{}
	doneOnce sync.Once
}

// NewStreamExternalWalletSignerClient creates a signer client with the default timeout of one hour.
func NewStreamExternalWalletSignerClient(sp SignerProvider, stream view2.Stream, _ int) *StreamExternalWalletSignerClient {
	return NewStreamExternalWalletSignerClientWithTimeout(sp, stream, 1*time.Hour)
}

// NewStreamExternalWalletSignerClientWithTimeout creates a signer client that gives up waiting
// for the next signature request from the remote wallet after the passed timeout.
func NewStreamExternalWalletSignerClientWithTimeout(sp SignerProvider, stream view2.Stream, timeout time.Duration) *StreamExternalWalletSignerClient {
	c := &StreamExternalWalletSignerClient{
		sp:      sp,
		stream:  stream,
		timeout: timeout,
		input:   make(chan *StreamExternalWalletSignRequest),
		err:     make(chan error, 1),
		done:    make(chan struct{}),
	}
	go c.init()

	return c
}

func (s *StreamExternalWalletSignerClient) init() {
	i := 0
	for {
		logger.Debugf("process signature request [%d]", i)

		msg := &StreamExternalWalletMsg{}
		if err := s.stream.Recv(msg); err != nil {
			s.err <- errors.Wrapf(err, "failed to receive signature request [%d]", i)

			return
		}
		switch msg.Type {
		case SigRequest:
			req := &StreamExternalWalletSignRequest{}
			if err := json.Unmarshal(msg.Raw, req); err != nil {
				s.err <- errors.Wrapf(err, "failed to get unmarshal msg type SigRequest")

				return
			} else {
				select {
				case s.input <- req:
				case <-s.done:
					logger.Debugf("no responder left for signature request [%d], giving up", i)

					return
				}
			}
		case Done:
			logger.Debugf("no more signatures required")
			close(s.input)

			return
		}
		i++
	}
}

// Respond serves the signature requests sent by the remote wallet until the wallet signals
// that it is done, a signature cannot be produced, or the stream fails. It returns nil once
// the remote wallet is done, and otherwise the failure that terminated the exchange: errors
// hit while receiving or decoding requests are reported by init on s.err and returned here,
// so the caller sees the real transport or decoding failure rather than a timeout.
//
// On return it signals the receive goroutine to stop, so that goroutine never lingers waiting
// to hand over a request that will no longer be served.
func (s *StreamExternalWalletSignerClient) Respond() error {
	defer s.stopConsuming()

	for {
		select {
		case req, done := <-s.input:
			if !done {
				return nil
			}
			msg, err := s.sign(req)
			if err != nil {
				return errors.Wrapf(err, "failed to marshal sign request message")
			}
			if err := s.stream.Send(msg); err != nil {
				return errors.Wrapf(err, "failed to send back signature, party [%s]", req.Party)
			}
			logger.Debugf("process signature request done")
		case err := <-s.err:
			return err
		case <-time.After(s.timeout):
			return errors.Errorf("Timeout waiting for stream input exceeded: %v", s.timeout)
		}
	}
}

// stopConsuming tells init that no further signature request will be consumed. It is safe to
// call more than once.
func (s *StreamExternalWalletSignerClient) stopConsuming() {
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *StreamExternalWalletSignerClient) sign(req *StreamExternalWalletSignRequest) (*StreamExternalWalletMsg, error) {
	signer, err := s.sp.GetSigner(req.Party)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get signer for party [%s]", req.Party)
	}
	sigma, err := signer.Sign(req.Message)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to sign, party [%s]", req.Party)
	}

	return NewStreamExternalWalletMsg(SignResponse, &StreamExternalWalletSignResponse{
		Sigma: sigma,
	})
}
