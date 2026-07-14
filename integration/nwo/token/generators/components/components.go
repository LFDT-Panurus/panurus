/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package components

type BuilderClient interface {
	Build(path string) string
}

type Builder struct {
	client BuilderClient
}

func NewBuilder(client BuilderClient) *Builder {
	return &Builder{client: client}
}

func (c *Builder) Build(path string) string {
	return c.client.Build(path)
}

func (c *Builder) FSCCLI() string {
	return c.Build("github.com/hyperledger-labs/fabric-smart-client/integration/nwo/cmd/fsccli")
}
