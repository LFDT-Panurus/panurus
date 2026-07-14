/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/
package certifier

import (
	"github.com/LFDT-Panurus/panurus/token/services/certifier/driver"
	"github.com/LFDT-Panurus/panurus/token/services/utils/drivers"
)

var holder = drivers.NewHolder[string, driver.Driver]()
