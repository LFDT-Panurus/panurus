/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package gateway

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
)

const (
	// defaultContainerPort is the in-container JSON-RPC port assumed by Launch when unset.
	defaultContainerPort = 8545
)

// Mount is a bind mount from a host path into the container.
type Mount struct {
	// HostPath is the absolute path on the host to bind.
	HostPath string
	// ContainerPath is the path inside the container where HostPath is mounted.
	ContainerPath string
	// ReadOnly, when true, mounts the bind read-only (appends ":ro").
	ReadOnly bool
}

// ContainerSpec describes how to run a single EVM node container via the docker CLI; only Image is required.
type ContainerSpec struct {
	// Image is the docker image reference for the EVM node. Required.
	Image string
	// Entrypoint, when non-empty, overrides the image entrypoint.
	Entrypoint string
	// Cmd holds the command arguments passed after the image name.
	Cmd []string
	// Env holds "KEY=VAL" environment entries, each passed via "-e".
	Env []string
	// Mounts holds bind mounts, each passed via "-v host:container[:ro]".
	Mounts []Mount
	// HostPort is the host port to publish the RPC endpoint on; 0 publishes an ephemeral port.
	HostPort int
	// ContainerPort is the in-container RPC port; 0 uses defaultContainerPort.
	ContainerPort int
}

// Node is a running EVM node container started by Launch.
type Node struct {
	// id is the docker container id returned by "docker run -d".
	id string
	// hostPort is the resolved host port on which the RPC endpoint is published.
	hostPort int
}

// Endpoint returns the JSON-RPC URL for the node.
func (n *Node) Endpoint() string {
	return "http://" + defaultHost + ":" + strconv.Itoa(n.hostPort)
}

// ContainerID returns the docker container id of the running node.
func (n *Node) ContainerID() string {
	return n.id
}

// Logs returns the combined stdout/stderr log output of the container via "docker logs".
func (n *Node) Logs(ctx context.Context) (string, error) {
	out, errOut, err := runDocker(ctx, "logs", n.id)
	if err != nil {
		return "", errors.Wrapf(err, "docker logs %s failed: %s", n.id, strings.TrimSpace(errOut))
	}

	return out + errOut, nil
}

// Stop force-removes the container via "docker rm -f"; it is idempotent for an already-absent container.
func (n *Node) Stop(ctx context.Context) error {
	_, errOut, err := runDocker(ctx, "rm", "-f", n.id)
	if err != nil {
		if strings.Contains(strings.ToLower(errOut), "no such container") {
			return nil
		}

		return errors.Wrapf(err, "docker rm -f %s failed: %s", n.id, strings.TrimSpace(errOut))
	}

	return nil
}

// buildRunArgs builds the "docker run" argument slice from spec in a deterministic, unit-testable order.
func buildRunArgs(spec ContainerSpec) []string {
	containerPort := spec.ContainerPort
	if containerPort == 0 {
		containerPort = defaultContainerPort
	}

	args := []string{"run", "-d"}

	if spec.Entrypoint != "" {
		args = append(args, "--entrypoint", spec.Entrypoint)
	}

	for _, e := range spec.Env {
		args = append(args, "-e", e)
	}

	for _, m := range spec.Mounts {
		v := m.HostPath + ":" + m.ContainerPath
		if m.ReadOnly {
			v += ":ro"
		}
		args = append(args, "-v", v)
	}

	cp := strconv.Itoa(containerPort)
	if spec.HostPort > 0 {
		args = append(args, "-p", defaultHost+":"+strconv.Itoa(spec.HostPort)+":"+cp)
	} else {
		args = append(args, "-p", defaultHost+"::"+cp)
	}

	args = append(args, spec.Image)
	args = append(args, spec.Cmd...)

	return args
}

// parseHostPort returns the first parseable published host port from "docker port" output.
func parseHostPort(out string, containerPort int) (int, error) {
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip a leading "<port>/tcp -> " mapping prefix if present.
		if idx := strings.LastIndex(line, "->"); idx >= 0 {
			line = strings.TrimSpace(line[idx+2:])
		}
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		portStr := strings.TrimSpace(line[colon+1:])
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > maxPort {
			continue
		}

		return port, nil
	}

	return 0, errors.Errorf("no published host port found for container port %d in %q", containerPort, out)
}

// runDocker executes "docker <args...>" and returns its stdout, stderr, and error separately.
func runDocker(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// Launch starts an EVM node container from spec, waits for its JSON-RPC endpoint to become reachable per cfg, and returns the running Node.
func Launch(ctx context.Context, spec ContainerSpec, cfg Config) (*Node, error) {
	if spec.ContainerPort == 0 {
		spec.ContainerPort = defaultContainerPort
	}
	if spec.Image == "" {
		return nil, errors.New("evm gateway launch: image must not be empty")
	}

	runArgs := buildRunArgs(spec)
	stdout, stderr, err := runDocker(ctx, runArgs...)
	if err != nil {
		return nil, errors.Wrapf(err, "docker run failed: %s", strings.TrimSpace(stderr))
	}

	id := strings.TrimSpace(stdout)
	if id == "" {
		return nil, errors.Errorf("docker run returned an empty container id (stderr: %s)", strings.TrimSpace(stderr))
	}

	hostPort := spec.HostPort
	if hostPort == 0 {
		portOut, portErr, perr := runDocker(ctx, "port", id, strconv.Itoa(spec.ContainerPort)+"/tcp")
		if perr != nil {
			node := &Node{id: id}
			_ = node.Stop(ctx)

			return nil, errors.Wrapf(perr, "docker port %s %d/tcp failed: %s", id, spec.ContainerPort, strings.TrimSpace(portErr))
		}
		hostPort, err = parseHostPort(portOut, spec.ContainerPort)
		if err != nil {
			node := &Node{id: id}
			_ = node.Stop(ctx)

			return nil, errors.Wrapf(err, "failed to determine host port for container %s", id)
		}
	}

	node := &Node{id: id, hostPort: hostPort}

	if err := WaitReachable(ctx, node.Endpoint(), cfg.ChainID, cfg); err != nil {
		logs, logErr := node.Logs(ctx)
		if logErr != nil {
			logs = "<failed to collect logs: " + logErr.Error() + ">"
		}
		_ = node.Stop(ctx)

		return nil, errors.Wrapf(err, "evm node %s did not become reachable at %s; container logs tail:\n%s",
			id, node.Endpoint(), tail(logs, 4000))
	}

	return node, nil
}

// tail returns the last max bytes of s, prefixed with a truncation marker when s is longer than max.
func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}

	return "...(truncated)...\n" + s[len(s)-max:]
}
