/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package crypto

import (
	idemix "github.com/IBM/idemix/bccsp"
	"github.com/IBM/idemix/bccsp/keystore"

	//lint:ignore SA1019 the deprecation will be undone
	//nolint:staticcheck
	idemix3 "github.com/IBM/idemix/bccsp/schemes/dlog/crypto"
	"github.com/IBM/idemix/bccsp/schemes/dlog/crypto/translator/amcl"
	bccsp "github.com/IBM/idemix/bccsp/types"
	math "github.com/IBM/mathlib"
	math2 "github.com/LFDT-Panurus/panurus/token/core/common/crypto/math"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// NewKeyStore creates a new Idemix key store for the specified curve using the provided KVS backend.
func NewKeyStore(curveID math.CurveID, backend keystore.KVS) (bccsp.KeyStore, error) {
	curve, tr, _, err := GetCurveAndTranslator(curveID)
	if err != nil {
		return nil, err
	}
	keyStore := &keystore.KVSStore{
		Curve:      curve,
		KVS:        backend,
		Translator: tr,
	}

	return keyStore, nil
}

// NewBCCSP returns an instance of the idemix BCCSP for the given curve and kvsStore
func NewBCCSP(keyStore bccsp.KeyStore, curveID math.CurveID) (bccsp.BCCSP, error) {
	curve, tr, aries, err := GetCurveAndTranslator(curveID)
	if err != nil {
		return nil, err
	}

	var cryptoProvider bccsp.BCCSP
	if aries {
		cryptoProvider, err = idemix.NewAries(keyStore, curve, tr, true)
	} else {
		cryptoProvider, err = idemix.New(keyStore, curve, tr, true)
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed getting crypto provider")
	}

	return cryptoProvider, nil
}

// NewBCCSPWithDummyKeyStore creates an Idemix BCCSP with a dummy key store for testing.
func NewBCCSPWithDummyKeyStore(curveID math.CurveID) (bccsp.BCCSP, error) {
	curve, tr, aries, err := GetCurveAndTranslator(curveID)
	if err != nil {
		return nil, err
	}
	var cryptoProvider bccsp.BCCSP
	if aries {
		cryptoProvider, err = idemix.NewAries(&keystore.Dummy{}, curve, tr, true)
	} else {
		cryptoProvider, err = idemix.New(&keystore.Dummy{}, curve, tr, true)
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed getting crypto provider")
	}

	return cryptoProvider, nil
}

// GetCurveAndTranslator returns the curve, translator, and Aries flag for the given curve ID.
func GetCurveAndTranslator(curveID math.CurveID) (*math.Curve, idemix3.Translator, bool, error) {
	if curveID < 0 {
		return nil, nil, false, errors.New("invalid curve")
	}
	if curveID == math.BLS12_381_BBS {
		// switch to math.BLS12_381_BBS_GURVY
		logger.Warnf("selected curve BLS12_381_BBS, switching to BLS12_381_BBS_GURVY")
		curveID = math.BLS12_381_BBS_GURVY
	}

	// Validate curve ID before accessing the array to avoid panic
	var tr idemix3.Translator
	aries := false
	switch curveID {
	case math.BN254:
		tr = &amcl.Gurvy{C: math.Curves[curveID]}
	case math.BLS12_377_GURVY:
		tr = &amcl.Gurvy{C: math.Curves[curveID]}
	case math.FP256BN_AMCL:
		tr = &amcl.Fp256bn{C: math.Curves[curveID]}
	case math.FP256BN_AMCL_MIRACL:
		tr = &amcl.Fp256bnMiracl{C: math.Curves[curveID]}
	case math.BLS12_381_BBS_GURVY:
		tr = &amcl.Gurvy{C: math.Curves[curveID]}
		aries = true
	case math2.BLS12_381_BBS_GURVY_FAST_RNG:
		tr = &amcl.Gurvy{C: math.Curves[curveID]}
		aries = true
	default:
		return nil, nil, false, errors.Errorf("unsupported curve ID: %d", curveID)
	}

	return math.Curves[curveID], tr, aries, nil
}
