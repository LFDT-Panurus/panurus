/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package services

import (
	"github.com/LFDT-Panurus/panurus/token"
)

type ServiceManager[S any] interface {
	ServiceByTMSId(token.TMSID) (S, error)
}

func Key(tmsID token.TMSID) string { return tmsID.String() }
