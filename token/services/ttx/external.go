/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"encoding/json"
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
	input   chan *StreamExternalWalletSignRequest
	err     chan error
}

func NewStreamExternalWalletSignerClient(sp SignerProvider, stream view2.Stream, _ int) *StreamExternalWalletSignerClient {
	return NewStreamExternalWalletSignerClientWithTimeout(sp, stream, 1*time.Hour)
}

func NewStreamExternalWalletSignerClientWithTimeout(sp SignerProvider, stream view2.Stream, timeout time.Duration) *StreamExternalWalletSignerClient {
	c := &StreamExternalWalletSignerClient{
		sp:      sp,
		stream:  stream,
		timeout: timeout,
		input:   make(chan *StreamExternalWalletSignRequest),
		err:     make(chan error),
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
				s.input <- req
			}
		case Done:
			logger.Debugf("no more signatures required")
			close(s.input)

			return
		}
		i++
	}
}

func (s *StreamExternalWalletSignerClient) Respond() error {
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
		case <-time.After(s.timeout):
			return errors.Errorf("Timeout waiting for stream input exceeded: %v", s.timeout)
		}
	}
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
