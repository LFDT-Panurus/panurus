/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package pagination

import (
	"encoding/json"
	"fmt"

	"github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
)

type offset struct {
	Offset   int `json:"offset"`
	PageSize int `json:"pageSize"`
}

// Offset creates a pagination using OFFSET
func Offset(os, pageSize int) (*offset, error) {
	if os < 0 {
		return nil, fmt.Errorf("offset shoud be grater than zero. Offset: %d", os)
	}
	if pageSize < 0 {
		return nil, fmt.Errorf("page size shoud be grater than zero. pageSize: %d", pageSize)
	}

	return &offset{Offset: os, PageSize: pageSize}, nil
}

func (p *offset) GoToOffset(os int) (driver.Pagination, error) {
	if os < 0 {
		return Empty(), nil
	}

	return &offset{
		Offset:   os,
		PageSize: p.PageSize,
	}, nil
}

func (k *offset) Serialize() ([]byte, error) {
	ret, err := json.Marshal(k)

	return ret, err
}

func OffsetFromRaw(raw []byte) (*offset, error) {
	var k offset
	err := json.Unmarshal(raw, &k)
	if err != nil {
		return nil, err
	}

	return &k, nil
}

func (p *offset) GoToPage(pageNum int) (driver.Pagination, error) {
	return p.GoToOffset(pageNum * p.PageSize)
}

func (p *offset) GoForward(numOfpages int) (driver.Pagination, error) {
	return p.GoToOffset(p.Offset + (numOfpages * p.PageSize))
}

func (p *offset) GoBack(numOfpages int) (driver.Pagination, error) {
	return p.GoForward(-1 * numOfpages)
}

func (p *offset) Prev() (driver.Pagination, error) { return p.GoBack(1) }
func (p *offset) Next() (driver.Pagination, error) { return p.GoForward(1) }

// pageSize implements pageSized so ValidateLimited can bound the page uniformly.
func (p *offset) pageSize() int { return p.PageSize }

func (o *offset) Equal(other driver.Pagination) bool {
	otherOffset, ok := other.(*offset)
	if !ok {
		return false
	}

	return o.Offset == otherOffset.Offset &&
		o.PageSize == otherOffset.PageSize
}
