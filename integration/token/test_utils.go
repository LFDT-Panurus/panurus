/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token

import (
	"fmt"
	"os"

	"github.com/LFDT-Panurus/panurus/integration/nwo/fabricbuilder"
	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc/node"
	postgres2 "github.com/hyperledger-labs/fabric-smart-client/platform/view/services/storage/driver/sql/postgres"
	"github.com/onsi/ginkgo/v2"
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

// TestSuite builds a network with its generator and runs it for the specs of a suite.
type TestSuite struct {
	generator func() (*integration.Infrastructure, error)
	// II is the infrastructure of the network, nil when no network was generated, or after
	// TearDown.
	II *integration.Infrastructure
}

// StopInfrastructure stops ii if it was generated. It does nothing if ii is nil, or if ii
// has no NWO because its Generate never ran: it does not stop the BuildServer or remove the
// test directory of such an Infrastructure, which hold no ports in the start range and are
// cleaned up by CleanupBuildArtifacts and DeleteOnStart.
func StopInfrastructure(ii *integration.Infrastructure) {
	if ii == nil || ii.NWO == nil {
		return
	}
	ii.Stop()
}

// TearDown stops the network with StopInfrastructure and then clears II. It is safe to
// call on a network whose Setup failed part-way, or that was never set up, and it is
// idempotent. II is cleared only once Stop returns, so a TearDown whose Stop panics leaves
// II set and the final cleanup retries once (best effort; a process that hangs will hang
// again, bounded by NWO's StopEventuallyTimeout).
func (s *TestSuite) TearDown() {
	if s.II == nil {
		return
	}
	StopInfrastructure(s.II)
	s.II = nil
}

// CleanupBuildArtifacts deletes the binaries that FSC's build server keeps across
// the networks started by the current process. Call it from the AfterSuite of every
// suite that starts a network; TearDown no longer removes them.
func CleanupBuildArtifacts() {
	integration.CleanupBuildArtifacts()
}

// Setup builds and starts the network. If any step fails it panics, through the gomega
// fail handler that FSC's integration.New installs, which fails the current spec or node.
// It assigns II before generating and starting the network, so that
// TearDown can stop a network whose Setup failed part-way.
func (s *TestSuite) Setup() {
	// never reuse the infrastructure of a previous network
	s.II = nil
	// Create the integration ii
	network, err := s.generator()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	s.II = network
	network.RegisterPlatformFactory(fabricbuilder.NewPlatformFactory())
	tokenPlatformFactory := token.NewPlatformFactory(s.II)
	network.RegisterPlatformFactory(tokenPlatformFactory)
	network.Generate()
	network.Start()
	// the public parameters of the fabricx backend can only be installed once the FSC nodes are
	// up, and InstallPendingPublicParams fails the suite if any installation failed
	tokenPlatformFactory.InstallPendingPublicParams()
}

// Network is a test network that a suite sets up and tears down, such as *TestSuite or
// a suite embedding FSC's *integration.TestSuite.
type Network interface {
	// Setup builds and starts the network.
	Setup()
	// TearDown stops the network. It must be safe to call on a network whose Setup failed
	// part-way, or that was never set up, and it must be idempotent.
	TearDown()
}

// ShareNetwork makes the specs of the enclosing Ordered, ContinueOnFailure container share
// one network, instead of the BeforeEach(ts.Setup)/AfterEach(ts.TearDown) pair that builds
// a network per spec. It is the Panurus counterpart of FSC's faultrecovery.ShareNetwork.
//
// Each spec in that container must leave the network healthy: every node it stops or
// kills is running again, and the public parameters and node configuration are unchanged.
// It must also not depend on the ledger state that the previous specs leave behind: the
// fungible checks assert absolute balances, holdings and issued totals, so two specs that
// issue or move the same token type to the same wallet cannot share a network. A spec
// that changes the network for good, such as a public parameters update or an identity
// revocation, may share it only as the last spec of the container.
//
// The network is built in BeforeAll. The DeferCleanup that tears it down is registered
// before the build starts, so a network whose build failed part-way is torn down too, and
// it runs after the DeferCleanup of the last spec. If the initial build fails, ginkgo fails
// that spec and skips the rest of the container. Under --fail-fast, ginkgo skips every spec
// after the first failure and never runs that DeferCleanup, so the network is torn down
// right after the spec that failed.
//
// A spec that fails, including in its own DeferCleanup, may leave the network broken, so
// the network is torn down and rebuilt before the next spec runs. If that rebuild fails,
// the spec it runs for fails before its body runs, and every later spec of the container
// is skipped rather than retrying the rebuild; the failed spec keeps the run red.
//
// When a label filter selects a single spec of the container, the network is built for that
// spec alone, so every label stays runnable on its own.
//
// If the ginkgo suite timeout (default 1h) expires, the network of the timed-out container
// is not torn down, as with FSC's faultrecovery.ShareNetwork. A CI runner is discarded
// anyway; locally, run `make clean-all-containers`.
func ShareNetwork(s Network) {
	st := &sharedNetwork{}
	ginkgo.BeforeAll(func() {
		ginkgo.DeferCleanup(s.TearDown)
		s.Setup()
	})
	ginkgo.BeforeEach(func() {
		// registered first, so it runs after the cleanups the specs of this container
		// register themselves
		ginkgo.DeferCleanup(func() {
			failed := ginkgo.CurrentSpecReport().Failed()
			st.afterSpec(failed)
			// under fail-fast ginkgo skips every remaining spec (ginkgo v2.33.1
			// internal/suite.go:449-451), so the BeforeAll cleanup would never run
			// (internal/group.go:303-311). TearDown is idempotent, so that later cleanup,
			// if it runs, is a no-op.
			suiteConfig, _ := ginkgo.GinkgoConfiguration()
			if tearDownNow(failed, suiteConfig.FailFast) {
				s.TearDown()
			}
		})
		if st.beforeEach(s) {
			ginkgo.Skip("network could not be rebuilt after an earlier failure")
		}
	})
}

// sharedNetwork tracks the health of the network shared by ShareNetwork across its specs.
type sharedNetwork struct {
	// broken is set when the last spec failed, so the network must be rebuilt.
	broken bool
	// dead is set when a rebuild did not complete, so no later spec can run.
	dead bool
}

// beforeEach rebuilds n if the previous spec left it broken, and reports whether the
// spec must be skipped because an earlier rebuild did not complete. If the rebuild fails,
// Setup or TearDown panics (as gomega does in a spec), the panic propagates and the
// network stays dead.
func (st *sharedNetwork) beforeEach(n Network) (skip bool) {
	if st.dead {
		return true
	}
	if st.broken {
		st.dead = true
		n.TearDown()
		n.Setup()
		st.dead = false
		st.broken = false
	}

	return false
}

// afterSpec records the outcome of a spec. A spec that ran on a live network leaves it
// broken if and only if it failed; a dead network stays dead whatever the outcome.
func (st *sharedNetwork) afterSpec(failed bool) {
	if st.dead {
		return
	}
	st.broken = failed
}

// tearDownNow reports whether the network must be torn down right after a spec: only if the
// spec failed under fail-fast, when ginkgo skips every later spec and the cleanup of the
// container never runs.
func tearDownNow(failed, failFast bool) bool {
	return failed && failFast
}
