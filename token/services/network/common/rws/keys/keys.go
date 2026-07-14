/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package keys

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/LFDT-Panurus/panurus/token/services/network/common/rws/translator"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	minUnicodeRuneValue   = 0 // U+0000
	compositeKeyNamespace = "\x00"
	maxUnicodeRuneValue   = utf8.MaxRune // U+10FFFF - maximum (and unallocated) code point
	numComponentsInKey    = 2            // 2 components: txid, index, excluding TokenKeyPrefix

	OutputSNKeyPrefix            = "osn"
	TokenSetupKeyPrefix          = "se"
	TokenSetupHashKeyPrefix      = "seh"
	TokenRequestKeyPrefix        = "tr"
	InputSerialNumberPrefix      = "sn"
	IssueActionMetadataPrefix    = "iam"
	TransferActionMetadataPrefix = "tam"
)

type Translator struct {
}

func (t *Translator) CreateTokenRequestKey(id string) (translator.Key, error) {
	return createCompositeKey(TokenRequestKeyPrefix, []string{id})
}

func (t *Translator) CreateSetupKey() (translator.Key, error) {
	return createCompositeKey(TokenSetupKeyPrefix, nil)
}

func (t *Translator) CreateSetupHashKey() (translator.Key, error) {
	return createCompositeKey(TokenSetupHashKeyPrefix, nil)
}

func (t *Translator) CreateOutputSNKey(id string, index uint64, output []byte) (translator.Key, error) {
	hf := sha256.New()
	hf.Write([]byte(OutputSNKeyPrefix))
	hf.Write([]byte(id))
	indexBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(indexBytes, index)
	hf.Write(indexBytes)
	hf.Write(output)

	return createCompositeKey(OutputSNKeyPrefix, []string{hex.EncodeToString(hf.Sum(nil))})
}

func (t *Translator) CreateOutputKey(id string, index uint64) (translator.Key, error) {
	return createCompositeKey(id, []string{strconv.FormatUint(index, 10)})
}

func (t *Translator) GetTransferMetadataSubKey(k string) (translator.Key, error) {
	prefix, components, err := splitCompositeKey(k)
	if err != nil {
		return "", errors.Wrapf(err, "failed to split composite key [%s]", k)
	}
	if len(components) != 1 {
		return "", errors.Wrapf(err, "key [%s] should contain 1 component, got [%d]", k, len(components))
	}
	if prefix != TransferActionMetadataPrefix {
		return "", errors.Errorf("key [%s] doesn not contain the token key prefix", k)
	}

	return components[0], nil
}

func (t *Translator) CreateInputSNKey(id string) (translator.Key, error) {
	return createCompositeKey(InputSerialNumberPrefix, []string{id})
}

func (t *Translator) CreateIssueActionMetadataKey(key string) (translator.Key, error) {
	return createCompositeKey(IssueActionMetadataPrefix, []string{key})
}

func (t *Translator) CreateTransferActionMetadataKey(key string) (translator.Key, error) {
	return createCompositeKey(TransferActionMetadataPrefix, []string{key})
}

func (t *Translator) TransferActionMetadataKeyPrefix() (translator.Key, error) {
	return createCompositeKey(TransferActionMetadataPrefix, nil)
}

// createCompositeKey and its related functions and consts copied from core/chaincode/shim/chaincode.go
func createCompositeKey(objectType string, attributes []string) (translator.Key, error) {
	if err := validateCompositeKeyAttribute(objectType); err != nil {
		return "", err
	}
	ck := compositeKeyNamespace + objectType + string(rune(minUnicodeRuneValue))
	var ckSb103 strings.Builder
	for _, att := range attributes {
		if err := validateCompositeKeyAttribute(att); err != nil {
			return "", err
		}
		ckSb103.WriteString(att + string(rune(minUnicodeRuneValue)))
	}
	ck += ckSb103.String()

	return ck, nil
}

func validateCompositeKeyAttribute(str string) error {
	if !utf8.ValidString(str) {
		return errors.Errorf("not a valid utf8 string: [%x]", str)
	}
	for index, runeValue := range str {
		if runeValue == minUnicodeRuneValue || runeValue == maxUnicodeRuneValue {
			return errors.Errorf(`input contain unicode %#U starting at position [%d]. %#U and %#U are not allowed in the input attribute of a composite key`,
				runeValue, index, minUnicodeRuneValue, maxUnicodeRuneValue)
		}
	}

	return nil
}

func splitCompositeKey(compositeKey string) (translator.Key, []string, error) {
	componentIndex := 1
	var components []string
	for i := 1; i < len(compositeKey); i++ {
		if compositeKey[i] == minUnicodeRuneValue {
			components = append(components, compositeKey[componentIndex:i])
			componentIndex = i + 1
		}
	}
	// there is an extra tokenIdPrefix component in the beginning, trim it off
	if len(components) < numComponentsInKey+1 {
		return "", nil, errors.Errorf("invalid composite key - not enough components found in key '%s', [%d][%v]", compositeKey, len(components), components)
	}

	return components[0], components[1:], nil
}
