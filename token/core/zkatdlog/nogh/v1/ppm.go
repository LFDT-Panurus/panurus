/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package v1

import (
	common2 "github.com/LFDT-Panurus/panurus/token/core/common"
	"github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/setup"
)

//go:generate counterfeiter -o mock/ppm.go -fake-name PublicParametersManager . PublicParametersManager

type PublicParametersManager = common2.PublicParametersManager[*setup.PublicParams]
