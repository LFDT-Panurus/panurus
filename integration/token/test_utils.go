/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"fmt"
	"os"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	postgres2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/onsi/gomega"
)

type ReplicationOpts interface {
	For(name string) []node.Option
}

const None = 0

type ReplicationOptions struct {
	*integration.ReplicationOptions
}

func NoReplication() (*ReplicationOptions, *ReplicaSelector) {
	return &ReplicationOptions{ReplicationOptions: integration.NoReplication}, &ReplicaSelector{}
}

func NewReplicationOptions(factor int, names ...string) (*ReplicationOptions, *ReplicaSelector) {
	if factor < None {
		panic("wrong factor")
	}
	if factor == None {
		return NoReplication()
	}
	replicationFactors := make(ReplicaSelector, len(names))
	sqlConfigs := make(map[string]*postgres2.ContainerConfig, len(names))
	for _, name := range names {
		replicationFactors[name] = factor
		sqlConfigs[name] = postgres2.DefaultConfig(postgres2.WithDBName(name + "-db"))
	}

	return &ReplicationOptions{ReplicationOptions: &integration.ReplicationOptions{
		ReplicationFactors: replicationFactors,
		SQLConfigs:         sqlConfigs,
	}}, &replicationFactors
}

type NodeReference struct {
	name          string
	replicaIdx    int
	totalReplicas int
}

func (r *NodeReference) String() string {
	return fmt.Sprintf("node [%s:%d:%d]", r.name, r.replicaIdx, r.totalReplicas)
}

func (r *NodeReference) ReplicaName() string {
	if r.totalReplicas <= 1 {
		return r.name
	}
	r.replicaIdx = (r.replicaIdx + 1) % r.totalReplicas

	return replicaName(r.name, r.replicaIdx)
}

func (r *NodeReference) AllNames() []string {
	if r.totalReplicas <= 1 {
		return []string{r.name}
	}
	replicaNames := make([]string, 0)
	for idx := range r.totalReplicas {
		replicaNames = append(replicaNames, replicaName(r.name, idx))
	}

	return replicaNames
}

func (r *NodeReference) Id() string { return r.name }

type ReplicaSelector map[string]int

func (s *ReplicaSelector) Get(name string) *NodeReference {
	return &NodeReference{name: name, totalReplicas: (*s)[name]}
}

func (s *ReplicaSelector) All(names ...string) []string {
	replicaNames := make([]string, 0)
	for _, name := range names {
		replicaNames = append(replicaNames, s.Get(name).AllNames()...)
	}

	return replicaNames
}

func AllNames(refs ...*NodeReference) []string {
	replicaNames := make([]string, 0)
	for _, ref := range refs {
		replicaNames = append(replicaNames, ref.AllNames()...)
	}

	return replicaNames
}

func replicaName(name string, idx int) string {
	return fmt.Sprintf("fsc.%s.%d", name, idx)
}

func NewTestSuite(startPort func() int, topologies []api.Topology) *TestSuite {
	return &TestSuite{
		generator: func() (*integration.Infrastructure, error) {
			i, err := integration.New(startPort(), "", topologies...)
			if err != nil {
				return i, err
			}
			if integration.WithRaceDetection {
				i.EnableRaceDetector()
			}

			return i, nil
		},
	}
}

// NewLocalTestSuite returns a new test suite that stores configuration data in `./testdata` and does not remove it when
// the test is done.
func NewLocalTestSuite(startPort func() int, topologies []api.Topology) *TestSuite {
	return &TestSuite{
		generator: func() (*integration.Infrastructure, error) {
			i, err := integration.New(startPort(), "./testdata", topologies...)
			if err != nil {
				return i, err
			}
			i.DeleteOnStop = false
			if integration.WithRaceDetection {
				i.EnableRaceDetector()
			}

			return i, nil
		},
	}
}

// RunnerDebug returns the passed string if RUNNER_DEBUG is not set to "1", otherwise it returns "debug"
func RunnerDebug(current string) string {
	if os.Getenv("RUNNER_DEBUG") == "1" {
		return "debug"
	}

	return current
}

type TestSuite struct {
	generator func() (*integration.Infrastructure, error)
	II        *integration.Infrastructure
}

func (s *TestSuite) TearDown() {
	s.II.Stop()
}

func (s *TestSuite) Setup() {
	// Create the integration ii
	network, err := s.generator()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	s.II = network
	network.RegisterPlatformFactory(token.NewPlatformFactory(s.II))
	network.Generate()
	network.Start()
}
