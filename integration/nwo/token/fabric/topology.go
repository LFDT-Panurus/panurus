/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

import "github.com/LFDT-Panurus/panurus/integration/nwo/token/topology"

func SetOrgs(tms *topology.TMS, orgs ...string) *topology.TMS {
	tms.BackendParams["fabric.orgs"] = orgs

	return tms
}

func GetOrgs(tms *topology.TMS) []string {
	v, ok := tms.BackendParams["fabric.orgs"]
	if !ok {
		return nil
	}
	orgs, ok := v.([]string)
	if !ok {
		panic("invalid value for fabric.orgs")
	}

	return orgs
}
