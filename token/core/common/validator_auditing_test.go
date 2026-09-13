/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"testing"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/hyperledger-labs/fabric-smart-client/platform/common/services/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type (
	TestContext = Context[driver.PublicParameters, driver.Input, driver.TransferAction, driver.IssueAction, driver.Deserializer]
	TestCheck   = func() bool
)

func TestAuditingSignaturesValidate(t *testing.T) {
	tests := []struct {
		name    string
		err     bool
		errMsg  string
		context func() (*TestContext, TestCheck)
	}{
		{
			name:   "No auditors but token requests with auditor signatures",
			err:    true,
			errMsg: ErrAuditorSignaturesPresent.Error(),
			context: func() (*TestContext, TestCheck) {
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns(nil)

				return &TestContext{
					PP: pp,
					TokenRequest: &driver.TokenRequest{
						Signatures: []*driver.RequestSignature{
							{
								Auditor: &driver.AuditorSignature{
									Identity:  driver.Identity("auditor"),
									Signature: []byte("auditor's signature"),
								},
							},
						},
					},
				}, nil
			},
		},
		{
			name: "No auditors and no token requests with auditor signatures",
			err:  false,
			context: func() (*TestContext, TestCheck) {
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns(nil)

				return &TestContext{
					PP:           pp,
					TokenRequest: &driver.TokenRequest{},
				}, nil
			},
		},
		{
			name:   "it is not an auditor",
			err:    true,
			errMsg: "auditor [LERVQYVKJM22xRRnp0G1rmcuYpOTY4x0mWJ5V21ZQ5I=] is not in auditors",
			context: func() (*TestContext, TestCheck) {
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns([]identity.Identity{driver.Identity("auditor1")})

				return &TestContext{
					PP: pp,
					TokenRequest: &driver.TokenRequest{
						Signatures: []*driver.RequestSignature{
							{
								Auditor: &driver.AuditorSignature{
									Identity:  driver.Identity("auditor2"),
									Signature: []byte("auditor 2's signature"),
								},
							},
						},
					},
				}, nil
			},
		},
		{
			name:   "it is an auditor but I cannot deserialize it",
			err:    true,
			errMsg: "failed to deserialize auditor's public key: auditor deserialize fail",
			context: func() (*TestContext, TestCheck) {
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns([]identity.Identity{driver.Identity("auditor")})

				des := &mock.Deserializer{}
				des.GetAuditorVerifierReturns(nil, errors.Errorf("auditor deserialize fail"))

				return &TestContext{
					PP: pp,
					TokenRequest: &driver.TokenRequest{
						Signatures: []*driver.RequestSignature{
							{
								Auditor: &driver.AuditorSignature{
									Identity:  driver.Identity("auditor"),
									Signature: []byte("auditor's signature"),
								},
							},
						},
					},
					Deserializer: des,
				}, nil
			},
		},
		{
			name:   "it is an auditor but no signatures to verify",
			err:    true,
			errMsg: ErrAuditorSignaturesMissing.Error(),
			context: func() (*TestContext, TestCheck) {
				auditor := driver.Identity("auditor")
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns([]identity.Identity{auditor})
				ver := &mock.Verifier{}
				ver.VerifyReturns(errors.New("signature is not valid"))
				des := &mock.Deserializer{}
				des.GetAuditorVerifierReturns(ver, nil)
				sp := &mock.SignatureProvider{}
				sp.HasBeenSignedByReturns(nil, errors.New("signature is not valid"))

				return &TestContext{
					PP: pp,
					TokenRequest: &driver.TokenRequest{
						Signatures: nil,
					},
					Deserializer:      des,
					SignatureProvider: sp,
				}, nil
			},
		},
		{
			name:   "it is an auditor but I cannot verify its signature",
			err:    true,
			errMsg: "failed to verify auditor's signature: signature is not valid",
			context: func() (*TestContext, TestCheck) {
				auditor := driver.Identity("auditor")
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns([]identity.Identity{auditor})
				ver := &mock.Verifier{}
				ver.VerifyReturns(errors.New("signature is not valid"))
				des := &mock.Deserializer{}
				des.GetAuditorVerifierReturns(ver, nil)
				sp := &mock.SignatureProvider{}
				sp.HasBeenSignedByReturns(nil, errors.New("signature is not valid"))

				return &TestContext{
						PP: pp,
						TokenRequest: &driver.TokenRequest{
							Signatures: []*driver.RequestSignature{
								{
									Auditor: &driver.AuditorSignature{
										Identity:  auditor,
										Signature: []byte("auditor's signature"),
									},
								},
							},
						},
						Deserializer:      des,
						SignatureProvider: sp,
					}, func() bool {
						_, id, ver2 := sp.HasBeenSignedByArgsForCall(0)
						if ver2 != ver {
							return false
						}

						return auditor.Equal(id)
					}
			},
		},
		{
			name: "it is an auditor and the signature is valid",
			err:  false,
			context: func() (*TestContext, TestCheck) {
				auditor := driver.Identity("auditor")
				pp := &mock.PublicParameters{}
				pp.AuditorsReturns([]identity.Identity{auditor})
				ver := &mock.Verifier{}
				ver.VerifyReturns(errors.New("signature is not valid"))
				des := &mock.Deserializer{}
				des.GetAuditorVerifierReturns(ver, nil)
				sp := &mock.SignatureProvider{}
				sp.HasBeenSignedByReturns(nil, nil)

				return &TestContext{
						PP: pp,
						TokenRequest: &driver.TokenRequest{
							Signatures: []*driver.RequestSignature{
								{
									Auditor: &driver.AuditorSignature{
										Identity:  auditor,
										Signature: []byte("auditor's signature"),
									},
								},
							},
						},
						Deserializer:      des,
						SignatureProvider: sp,
					}, func() bool {
						_, id, ver2 := sp.HasBeenSignedByArgsForCall(0)
						if ver2 != ver {
							return false
						}

						return auditor.Equal(id)
					}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, check := tt.context()
			err := AuditingSignaturesValidate(t.Context(), ctx)
			if tt.err {
				require.Error(t, err)
				require.EqualError(t, err, tt.errMsg)
			} else {
				require.NoError(t, err)
			}
			if check != nil {
				assert.True(t, check())
			}
		})
	}
}
