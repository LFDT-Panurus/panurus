/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// lazergen generates issuer keys and signer configurations for the lazer
// (post-quantum anonymous credential) identity service.
//
//	lazergen ca-keygen   -out DIR
//	lazergen signerconfig -ca DIR -out DIR -eid EID -ou OU -role ROLE [-rh RH]
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"

	"github.com/LFDT-Panurus/panurus/x/token/services/identity/lazer"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: lazergen ca-keygen|signerconfig [flags]")
	}
	switch args[0] {
	case "ca-keygen":
		fs := flag.NewFlagSet("ca-keygen", flag.ContinueOnError)
		out := fs.String("out", "", "output directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *out == "" {
			return errors.New("-out is required")
		}
		sk, pk := lazer.NewIssuerKey()

		return lazer.WriteIssuerKeys(*out, sk, pk)
	case "signerconfig":
		fs := flag.NewFlagSet("signerconfig", flag.ContinueOnError)
		ca := fs.String("ca", "", "issuer directory (from ca-keygen)")
		out := fs.String("out", "", "output directory")
		eid := fs.String("eid", "", "enrollment id")
		ou := fs.String("ou", "", "organizational unit")
		role := fs.String("role", "member", "role")
		rh := fs.String("rh", "", "revocation handle (random if empty)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *ca == "" || *out == "" || *eid == "" {
			return errors.New("-ca, -out and -eid are required")
		}
		if *rh == "" {
			b := make([]byte, 16)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			*rh = hex.EncodeToString(b)
		}
		sk, pk, err := lazer.ReadIssuerKeys(*ca)
		if err != nil {
			return err
		}
		signer, err := lazer.IssueSignerConfig(sk, pk, *ou, *role, *eid, *rh)
		if err != nil {
			return err
		}
		// the public key is needed to load the wallet, copy it next to the signer
		if err := lazer.WriteIssuerKeys(*out, nil, pk); err != nil {
			return err
		}
		// SignerConfigFull has the secrets; SignerConfig is the verify-only (remote) version
		if err := lazer.WriteSignerConfig(*out, signer, true); err != nil {
			return err
		}
		remote := *signer
		remote.Sk = nil

		return lazer.WriteSignerConfig(*out, &remote, false)
	default:
		return errors.Errorf("unknown command %q", args[0])
	}
}
