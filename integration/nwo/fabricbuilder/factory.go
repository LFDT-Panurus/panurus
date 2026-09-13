/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package fabricbuilder overrides NWO's default "fabric" platform factory so
// that every generated network registers panurus's own golang external
// builder (ci/external-builders/golang). Without it, peers build Go
// chaincode from source themselves inside a hyperledger/fabric-ccenv Docker
// image whose bundled Go toolchain is pinned to the corresponding Fabric
// release and can lag behind the Go version fabric-smart-client requires.
package fabricbuilder

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric/fabricconfig"
	"github.com/onsi/gomega"
)

// propagatedEnv is passed through to the external builder's build/run
// scripts, matching what compiling and running Go code on the host requires.
var propagatedEnv = []string{
	"GOCACHE", "GOPATH", "GOMODCACHE", "GOENV", "GOPROXY", "GOSUMDB",
	"GOFLAGS", "GOTOOLCHAIN", "HOME", "PATH",
}

type platformFactory struct {
	inner api.PlatformFactory
}

// NewPlatformFactory returns a "fabric" api.PlatformFactory equivalent to
// fabric.NewPlatformFactory(), except every network it creates also has
// panurus's golang external builder registered.
func NewPlatformFactory() api.PlatformFactory {
	return &platformFactory{inner: fabric.NewPlatformFactory()}
}

func (f *platformFactory) Name() string {
	return f.inner.Name()
}

func (f *platformFactory) New(registry api.Context, t api.Topology, builder api.Builder) api.Platform {
	p, ok := f.inner.New(registry, t, builder).(*fabric.Platform)
	gomega.Expect(ok).To(gomega.BeTrue(), "fabric platform factory returned an unexpected type")

	path, err := builderPath()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to locate ci/external-builders/golang")

	p.Network.ExternalBuilders = append(p.Network.ExternalBuilders, fabricconfig.ExternalBuilder{
		Name:                 "golang",
		Path:                 path,
		PropagateEnvironment: propagatedEnv,
	})

	return p
}

// builderPath resolves ci/external-builders/golang relative to the
// integration module's root, independent of the caller's current working
// directory (each integration test suite runs from its own package
// directory).
func builderPath() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}

	moduleRoot := filepath.Dir(strings.TrimSpace(string(out)))

	return filepath.Join(moduleRoot, "..", "ci", "external-builders", "golang"), nil
}
