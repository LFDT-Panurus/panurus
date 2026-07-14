/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// export_test.go exposes unexported fields and helpers for use by the external
// test package (package interactive_test). Compiled only during testing.
package interactive

import (
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
)

// ClientTokensChan returns the raw tokens channel of a CertificationClient.
func ClientTokensChan(cc *CertificationClient) chan *token.ID { return cc.tokens }

// ClientProcessBatch calls the unexported processBatch method.
func ClientProcessBatch(cc *CertificationClient, ids []*token.ID) { cc.processBatch(ids) }

// ServiceBackend returns the backend wired into a CertificationService.
func ServiceBackend(s *CertificationService) Backend { return s.backend }

// ServiceWallets returns the wallets map of a CertificationService.
func ServiceWallets(s *CertificationService) map[string]string { return s.wallets }

// ServiceMetrics returns the metrics of a CertificationService.
func ServiceMetrics(s *CertificationService) *Metrics { return s.metrics }

// CRVNetwork returns the network field of a CertificationRequestView.
func CRVNetwork(v *CertificationRequestView) string { return v.network }

// CRVChannel returns the channel field of a CertificationRequestView.
func CRVChannel(v *CertificationRequestView) string { return v.channel }

// CRVNamespace returns the ns field of a CertificationRequestView.
func CRVNamespace(v *CertificationRequestView) string { return v.ns }

// CRVCertifier returns the certifier field of a CertificationRequestView.
func CRVCertifier(v *CertificationRequestView) view.Identity { return v.certifier }

// CRVIDs returns the ids field of a CertificationRequestView.
func CRVIDs(v *CertificationRequestView) []*token.ID { return v.ids }
