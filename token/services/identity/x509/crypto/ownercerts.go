/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// maxOwnerCACerts caps the number of entries in each owner CA list.
	maxOwnerCACerts = 64
	// maxOwnerCACertSize caps the size of each PEM entry.
	maxOwnerCACertSize = 64 * 1024
	// maxChainIntermediates caps the number of intermediates in a chain.
	maxChainIntermediates = 8
)

// pemBegin starts every PEM block.
var pemBegin = []byte("-----BEGIN ")

// oidNameConstraints is the X.509 name constraints extension (RFC 5280, 4.2.1.10).
var oidNameConstraints = asn1.ObjectIdentifier{2, 5, 29, 30}

// OwnerCAs holds the parsed x509 owner CA certificates of the public parameters.
type OwnerCAs struct {
	roots         []*x509.Certificate
	intermediates []*x509.Certificate
}

// NewOwnerCAs checks and parses the x509 owner CA certificates of the public parameters.
// Each entry must be exactly one PEM CERTIFICATE block with no headers and no bytes around it,
// at most 64 KiB, and a CA (basic constraints, CertSign if key usage is set, no name constraints).
// Each list holds at most 64 entries and no certificate may appear twice.
// Every intermediate must chain to a root by signature, through at most 8 intermediates,
// honouring MaxPathLen. Validity periods are not checked.
// Intermediates without roots are rejected. It returns nil when both lists are empty.
func NewOwnerCAs(roots, intermediates [][]byte) (*OwnerCAs, error) {
	if len(roots) == 0 {
		if len(intermediates) != 0 {
			return nil, errors.New("x509 owner intermediates set without roots")
		}

		return nil, nil
	}
	rootCerts, err := parseCACertificates("root", roots)
	if err != nil {
		return nil, err
	}
	intermediateCerts, err := parseCACertificates("intermediate", intermediates)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rootCerts)+len(intermediateCerts))
	for _, c := range append(append([]*x509.Certificate{}, rootCerts...), intermediateCerts...) {
		if _, ok := seen[string(c.Raw)]; ok {
			return nil, errors.Errorf("duplicate x509 owner CA certificate [%s]", c.Subject)
		}
		seen[string(c.Raw)] = struct{}{}
	}
	for i, c := range intermediateCerts {
		if !chainsToRoot(c, intermediateCerts, rootCerts) {
			return nil, errors.Errorf("x509 owner intermediate [%d] does not chain to a root", i)
		}
	}

	return &OwnerCAs{roots: rootCerts, intermediates: intermediateCerts}, nil
}

// VerifyOwner checks that cert is not a CA and chains to a root by signature.
// Validity periods are not checked.
func (o *OwnerCAs) VerifyOwner(cert *x509.Certificate) error {
	if cert.IsCA {
		return errors.New("x509 owner certificate is a CA")
	}
	if !chainsToRoot(cert, o.intermediates, o.roots) {
		return errors.Errorf("x509 owner certificate [%s] does not chain to an owner root", cert.Subject)
	}

	return nil
}

// ParseOwnerCertificate parses an x509 owner certificate strictly:
// exactly one PEM CERTIFICATE block with no headers and no bytes around it, and an ECDSA public key.
func ParseOwnerCertificate(raw []byte) (*x509.Certificate, error) {
	c, err := parseStrictPEMCertificate(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := c.PublicKey.(*ecdsa.PublicKey); !ok {
		return nil, errors.Errorf("unsupported x509 owner public key type [%T]", c.PublicKey)
	}

	return c, nil
}

// parseCACertificates parses and checks a list of owner CA certificates.
func parseCACertificates(kind string, raws [][]byte) ([]*x509.Certificate, error) {
	if len(raws) > maxOwnerCACerts {
		return nil, errors.Errorf("too many x509 owner %s certificates [%d], max [%d]", kind, len(raws), maxOwnerCACerts)
	}
	certs := make([]*x509.Certificate, 0, len(raws))
	for i, raw := range raws {
		c, err := parseStrictPEMCertificate(raw)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid x509 owner %s [%d]", kind, i)
		}
		if !c.BasicConstraintsValid || !c.IsCA {
			return nil, errors.Errorf("x509 owner %s [%d] is not a CA", kind, i)
		}
		if c.KeyUsage != 0 && c.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, errors.Errorf("x509 owner %s [%d] lacks the CertSign key usage", kind, i)
		}
		for _, ext := range c.Extensions {
			if ext.Id.Equal(oidNameConstraints) {
				return nil, errors.Errorf("x509 owner %s [%d] has name constraints, not supported", kind, i)
			}
		}
		certs = append(certs, c)
	}

	return certs, nil
}

// parseStrictPEMCertificate parses exactly one PEM CERTIFICATE block with no headers and no bytes around it.
func parseStrictPEMCertificate(raw []byte) (*x509.Certificate, error) {
	if len(raw) > maxOwnerCACertSize {
		return nil, errors.Errorf("certificate too large [%d], max [%d]", len(raw), maxOwnerCACertSize)
	}
	// pem.Decode skips leading bytes and malformed blocks: reject them
	if !bytes.HasPrefix(raw, pemBegin) || bytes.Count(raw, pemBegin) != 1 {
		return nil, errors.New("expected exactly one PEM block and no leading bytes")
	}
	block, rest := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if block.Type != "CERTIFICATE" {
		return nil, errors.Errorf("unexpected PEM block type [%s]", block.Type)
	}
	if len(block.Headers) != 0 {
		return nil, errors.New("unexpected PEM headers")
	}
	if len(rest) != 0 {
		return nil, errors.New("trailing bytes after PEM block")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse certificate")
	}

	return c, nil
}

// chainsToRoot reports whether cert chains to one of roots via intermediates.
// Only signatures, CA flags and MaxPathLen are checked; validity periods are not.
func chainsToRoot(cert *x509.Certificate, intermediates, roots []*x509.Certificate) bool {
	caBelow := 0
	if cert.IsCA {
		caBelow = 1
	}

	return walkChain(cert, caBelow, 0, intermediates, roots, map[chainState]struct{}{})
}

// chainState identifies a DFS node: a certificate reached after a given number of intermediates.
type chainState struct {
	cert  *x509.Certificate
	depth int
}

// walkChain is a depth-first search towards a root.
// caBelow counts the CA certificates in the path below the next parent.
// visited records the states already explored; the result depends on the state only.
func walkChain(cert *x509.Certificate, caBelow, depth int, intermediates, roots []*x509.Certificate, visited map[chainState]struct{}) bool {
	for _, r := range roots {
		if isIssuedBy(cert, r, caBelow) {
			return true
		}
	}
	if depth >= maxChainIntermediates {
		return false
	}
	for _, ic := range intermediates {
		s := chainState{cert: ic, depth: depth + 1}
		if _, ok := visited[s]; ok {
			continue
		}
		if !isIssuedBy(cert, ic, caBelow) {
			continue
		}
		visited[s] = struct{}{}
		if walkChain(ic, caBelow+1, depth+1, intermediates, roots, visited) {
			return true
		}
	}

	return false
}

// isIssuedBy reports whether parent signed cert and allows caBelow CA certificates below it.
func isIssuedBy(cert, parent *x509.Certificate, caBelow int) bool {
	if cert == parent || !bytes.Equal(cert.RawIssuer, parent.RawSubject) {
		return false
	}
	if (parent.MaxPathLen > 0 || parent.MaxPathLenZero) && caBelow > parent.MaxPathLen {
		return false
	}

	return cert.CheckSignatureFrom(parent) == nil
}
