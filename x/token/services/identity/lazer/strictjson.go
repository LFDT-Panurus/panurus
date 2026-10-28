/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package lazer

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

// strictUnmarshal decodes raw into v and rejects what encoding/json accepts
// silently: unknown members, members whose name differs from the field name
// only by case, duplicate members, invalid UTF-8 and any data (including
// closing delimiters) after the top-level value.
func strictUnmarshal(raw []byte, v any) error {
	// encoding/json/v2 matches member names case-sensitively and rejects
	// duplicate names and trailing data by default
	return jsonv2.Unmarshal(raw, v, jsonv2.RejectUnknownMembers(true))
}

// canonicalUnmarshal is strictUnmarshal, plus the check that raw is exactly
// the encoding json.Marshal produces for the decoded value. It is meant for
// machine-produced encodings, so that every value has a single encoding.
func canonicalUnmarshal(raw []byte, v any) error {
	if err := strictUnmarshal(raw, v); err != nil {
		return err
	}
	again, err := json.Marshal(v)
	if err != nil {
		return errors.Wrap(err, "failed to re-encode")
	}
	if !bytes.Equal(again, raw) {
		return errors.New("non-canonical encoding")
	}

	return nil
}
