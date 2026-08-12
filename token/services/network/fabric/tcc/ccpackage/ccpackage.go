/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package ccpackage describes how the public parameters are baked into the
// token chaincode package. Everything that builds such a package (tokengen and
// the integration test harness) shares these definitions, so that a move of the
// chaincode sources cannot silently break one of them.
package ccpackage

const (
	// ChaincodePath is the import path of the token chaincode.
	ChaincodePath = "github.com/LFDT-Panurus/panurus/token/services/network/fabric/tcc/main"

	// ParamsFileSuffix is the path suffix of the source file holding the public
	// parameters. The packager passes the absolute path of every file it
	// packages, so this must match the tail of the path of tcc/params.go.
	ParamsFileSuffix = "/token/services/network/fabric/tcc/params.go"

	// ParamsTemplate is the source of the file that replaces the file matched by
	// ParamsFileSuffix. It must stay in sync with tcc/params.go.
	ParamsTemplate = `
package tcc

var Params = "{{ Params }}"
`
)
