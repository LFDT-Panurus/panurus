/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gen

import (
	"os"

	"github.com/LFDT-Panurus/panurus/integration/nwo/token"
	"github.com/hyperledger-labs/fabric-smart-client/integration"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/api"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fabric"
	"github.com/hyperledger-labs/fabric-smart-client/integration/nwo/fsc"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// Topology represents a topology.
type Topology struct {
	Type string `yaml:"type,omitempty"`
}

// Topologies represents a list of topologies.
type Topologies struct {
	Topologies []Topology `yaml:"topologies,omitempty"`
}

// T represents a list of topologies.
type T struct {
	Topologies []any `yaml:"topologies,omitempty"`
}

var topologyFile string
var output string
var port int

// Cmd returns the Cobra Command for generating artifacts.
func Cmd() *cobra.Command {
	// Set the flags on the node start command.
	flags := cobraCommand.Flags()
	flags.StringVarP(&topologyFile, "topology", "t", "", "topology file in yaml format")
	flags.StringVarP(&output, "output", "o", "./testdata", "output folder")
	flags.IntVarP(&port, "port", "p", 20000, "host starting port")

	return cobraCommand
}

var cobraCommand = &cobra.Command{
	Use:   "artifacts",
	Short: "Gen artifacts.",
	Long:  `Read topology from file and generates artifacts.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return errors.New("trailing args detected")
		}
		// Parsing of the command line is done so silence cmd usage
		cmd.SilenceUsage = true

		return gen(args)
	},
}

// gen read topology and generates artifacts
func gen(args []string) error {
	if len(topologyFile) == 0 {
		return errors.Errorf("expecting topology file path")
	}
	raw, err := os.ReadFile(topologyFile)
	if err != nil {
		return errors.Wrapf(err, "failed reading topology file [%s]", topologyFile)
	}
	t2, err := LoadTopologies(raw)
	if err != nil {
		return errors.Wrapf(err, "failed loading topologies from [%s]", topologyFile)
	}

	network, err := integration.New(port, output, t2...)
	if err != nil {
		return errors.Wrapf(err, "cannot instantate integration infrastructure")
	}
	network.RegisterPlatformFactory(token.NewPlatformFactory(nil))
	network.Generate()

	return nil
}

// LoadTopologies loads topologies from the given raw byte slice.
func LoadTopologies(raw []byte) ([]api.Topology, error) {
	names := &Topologies{}
	if err := yaml.Unmarshal(raw, names); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling topologies")
	}

	t := &T{}
	if err := yaml.Unmarshal(raw, t); err != nil {
		return nil, errors.Wrapf(err, "failed unmarshalling topologies")
	}
	t2 := []api.Topology{}
	for i, topology := range names.Topologies {
		var top api.Topology
		switch topology.Type {
		case fabric.TopologyName:
			top = fabric.NewDefaultTopology()
		case fsc.TopologyName:
			top = fsc.NewTopology()
		case token.TopologyName:
			top = token.NewTopology()
		default:
			continue
		}
		if err := remarshalTopology(t.Topologies[i], top); err != nil {
			return nil, err
		}
		t2 = append(t2, top)
	}

	return t2, nil
}

// remarshalTopology re-encodes entry (already parsed generically as part of T) to YAML
// and decodes it into top, so the topology-specific fields land on top's concrete type.
func remarshalTopology(entry any, top any) error {
	r, err := yaml.Marshal(entry)
	if err != nil {
		return errors.Wrapf(err, "failed remarshalling topology configuration")
	}
	if err := yaml.Unmarshal(r, top); err != nil {
		return errors.Wrapf(err, "failed unmarshalling topology")
	}

	return nil
}
