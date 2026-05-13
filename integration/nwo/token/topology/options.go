/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package topology

import (
	"fmt"

	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
)

type Options struct {
	Mapping map[string]any
}

func (o *Options) Certifier() bool {
	res := o.Mapping["Certifier"]
	if res == nil {
		return false
	}

	return res.(bool)
}

func (o *Options) SetCertifier(v bool) {
	o.Mapping["Certifier"] = v
}

func (o *Options) Issuers() []string {
	boxed := o.Mapping["Issuers"]
	if boxed == nil {
		return nil
	}
	res, ok := boxed.([]string)
	if ok {
		return res
	}
	res = []string{}
	for _, v := range boxed.([]any) {
		res = append(res, v.(string))
	}

	return res
}

func (o *Options) SetIssuers(ids []string) {
	o.Mapping["Issuers"] = ids
}

func (o *Options) Owners() []string {
	boxed := o.Mapping["Owners"]
	if boxed == nil {
		return nil
	}
	res, ok := boxed.([]string)
	if ok {
		return res
	}
	res = []string{}
	for _, v := range boxed.([]any) {
		res = append(res, v.(string))
	}

	return res
}

func (o *Options) SetOwners(ids []string) {
	o.Mapping["Owners"] = ids
}

// SetRemoteOwner marks the passed owner wallet identifier as remote
func (o *Options) SetRemoteOwner(id string) {
	o.Mapping["Owners.remote."+id] = true
}

// IsRemoteOwner returns true if the passed owner wallet identifier is marked as remote
func (o *Options) IsRemoteOwner(id string) bool {
	v, ok := o.Mapping["Owners.remote."+id]
	if !ok {
		return false
	}

	return v.(bool)
}

func (o *Options) Auditor() bool {
	res := o.Mapping["Auditor"]
	if res == nil {
		return false
	}

	return res.(bool)
}

func (o *Options) SetAuditor(v bool) {
	o.Mapping["Auditor"] = v
}

func (o *Options) Endorser() bool {
	res := o.Mapping["Endorser"]
	if res == nil {
		return false
	}

	return res.(bool)
}

func (o *Options) SetEndorser(v bool) {
	o.Mapping["Endorser"] = v
}

func (o *Options) UseHSMForIssuer(label string) {
	o.Mapping["Issuers.HSM."+label] = true
}

func (o *Options) IsUseHSMForIssuer(label string) bool {
	v, ok := o.Mapping["Issuers.HSM."+label]
	if !ok {
		return false
	}

	return v.(bool)
}

func (o *Options) UseHSMForAuditor() {
	o.Mapping["Auditor.HSM"] = true
}

func (o *Options) IsUseHSMForAuditor() bool {
	v, ok := o.Mapping["Auditor.HSM"]
	if !ok {
		return false
	}

	return v.(bool)
}

func ToOptions(o *node.Options) *Options {
	opt, ok := o.Mapping["token"]
	if !ok {
		opt = &Options{Mapping: map[string]any{}}
		o.Mapping["token"] = opt
	}
	res, ok := opt.(*Options)
	if ok {
		return res
	}
	// Handle map[any]any
	if mapping, ok := opt.(map[any]any); ok {
		return Convert(mapping)
	}

	// Handle map[string]any from JSON/YAML unmarshaling
	if mapping, ok := opt.(map[string]any); ok {
		anyMapping := make(map[any]any)
		for k, v := range mapping {
			anyMapping[k] = v
		}

		return Convert(anyMapping)
	}

	panic(fmt.Sprintf("invalid options type: %T", opt))
}

func Convert(m map[any]any) *Options {
	opts := &Options{
		Mapping: map[string]any{},
	}

	// Handle both nested "mapping" key and direct mapping
	var source map[any]any
	if mappingVal, ok := m["mapping"]; ok {
		if nestedMap, ok := mappingVal.(map[any]any); ok {
			source = nestedMap
		} else if nestedMap, ok := mappingVal.(map[string]any); ok {
			// Convert map[string]any to map[any]any
			source = make(map[any]any)
			for k, v := range nestedMap {
				source[k] = v
			}
		} else {
			panic(fmt.Sprintf("invalid nested mapping type: %T", mappingVal))
		}
	} else {
		// Use the entire map if no "mapping" key exists
		source = m
	}

	for k, v := range source {
		opts.Mapping[k.(string)] = v
	}

	return opts
}
