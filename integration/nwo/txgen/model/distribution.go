/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package model

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/LFDT-Panurus/panurus/integration/nwo/txgen/model/api"
)

type Distribution string

func (d *Distribution) GetAmounts(total api.Amount) ([]api.Amount, api.Error) {
	if strings.HasPrefix(string(*d), "const:") {
		return d.generateConstAmounts(total)
	} else if strings.HasPrefix(string(*d), "uniform:") {
		return d.generateUniformAmounts(total)
	}

	msg := fmt.Sprintf("Unknown type in distribution: %s", *d)
	// logging.Logger.Errorf(msg)

	return nil, api.NewBadRequestError(nil, msg)
}

func (d *Distribution) generateConstAmounts(total api.Amount) ([]api.Amount, api.Error) {
	cutSize := len("const:")
	inputAsChrs := strings.Split(string(*d)[cutSize:], ":")

	inputAsInt, err := d.convertToIntegers(inputAsChrs)
	if err != nil {
		return nil, err
	}

	amounts := make([]api.Amount, 0, 1)

	for total > 0 {
		for _, v := range inputAsInt {
			if v >= total {
				amounts = append(amounts, total)
				total = 0

				break
			}

			amounts = append(amounts, v)
			total -= v
		}
	}

	return amounts, nil
}

func (d *Distribution) generateUniformAmounts(total api.Amount) ([]api.Amount, api.Error) {
	cutSize := len("uniform:")
	inputAsChrs := strings.Split(string(*d)[cutSize:], ":")

	inputAsInt, err := d.convertToIntegers(inputAsChrs)
	if err != nil {
		return nil, err
	}

	minimum := inputAsInt[0]
	maximum := inputAsInt[1]
	if maximum < minimum {
		return nil, api.NewBadRequestError(nil, "maximum amount is too low")
	}
	diff := maximum - minimum
	amounts := make([]api.Amount, 0, 1)
	for total > 0 {
		r := rand.Uint64N(diff) + minimum
		if r >= total {
			amounts = append(amounts, total)

			break
		}

		amounts = append(amounts, r)
		total -= r
	}

	return amounts, nil
}

func (d *Distribution) convertToIntegers(input []string) ([]api.Amount, api.Error) {
	ints := make([]api.Amount, len(input))

	for i, v := range input {
		num, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			msg := fmt.Sprintf("Can't convert %s to intereger", v)
			// logging.Logger.Errorf(msg)
			return nil, api.NewBadRequestError(nil, msg)
		}
		ints[i] = num
	}

	return ints, nil
}
