/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package hashicorp

import (
	"fmt"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	vault "github.com/hashicorp/vault/api"
	docker2 "github.com/hyperledger-labs/fabric-smart-client/integration/nwo/common/docker"
	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const (
	ImageName           = "hashicorp/vault:latest"
	ContainerNamePrefix = "dev-hashicorp-vault-container-"
)

// NewVaultClient creates a new Vault client
func NewVaultClient(address, token string) (*vault.Client, error) {
	config := vault.DefaultConfig()
	config.Address = address

	client, err := vault.NewClient(config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Vault client")
	}

	client.SetToken(token)

	return client, nil
}

func StartHashicorpVaultContainer(t *testing.T, port int) (func(), string, string) {
	t.Helper()
	docker, err := docker2.GetInstance()
	if err != nil {
		t.Fatalf("failed to connect to docker daemon: %v", err)
	}
	err = docker.CheckImagesExist(ImageName)
	if err != nil {
		t.Fatal(err)
	}

	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	// Define the container configuration
	portStr := strconv.Itoa(port)
	token := "00000000-0000-0000-0000-000000000000"
	listenAddress := "0.0.0.0:" + portStr
	clientAddress := "127.0.0.1:" + portStr
	portBinding := network.MustParsePort(fmt.Sprintf("%d/tcp", port))
	containerConfig := &container.Config{
		Image: ImageName,
		Env: []string{
			"SKIP_SETCAP=true",
			"VAULT_DEV_ROOT_TOKEN_ID=" + token,
			"VAULT_DEV_LISTEN_ADDRESS=" + listenAddress,
		},
		ExposedPorts: network.PortSet{
			portBinding: {},
		},
	}

	// Define the host configuration
	hostConfig := &container.HostConfig{
		CapAdd: []string{"IPC_LOCK"},
		PortBindings: network.PortMap{ // Use network.PortMap for port bindings
			portBinding: []network.PortBinding{
				{
					HostIP:   netip.MustParseAddr("0.0.0.0"),
					HostPort: portStr,
				},
			},
		},
	}
	// Create the container
	ctx := t.Context()
	containerName := ContainerNamePrefix + portStr
	resp, err := cli.ContainerCreate(
		ctx,
		client.ContainerCreateOptions{
			Config:           containerConfig,
			HostConfig:       hostConfig,
			NetworkingConfig: nil,
			Platform:         nil,
			Name:             containerName,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}

	fmt.Println(resp.ID)

	// Wait for Vault to be ready using 127.0.0.1 (0.0.0.0 is a bind address, not routable by clients)
	vaultURL := "http://" + clientAddress
	if err := waitForVault(vaultURL, token); err != nil {
		t.Fatal(err)
	}

	// Enable the kv secret engine version 1
	if err := enableKVSecretEngine(vaultURL, token, "kv1"); err != nil {
		t.Fatal(err)
	}

	return func() {
		noWaitTimeout := 0 // to not wait for the container to exit gracefully
		if _, err := cli.ContainerStop(ctx, resp.ID, client.ContainerStopOptions{Timeout: &noWaitTimeout}); err != nil {
			logger.Errorf("failed to terminate hashicorp/vault [%s][%s]", err, string(debug.Stack()))
		}
		fmt.Println("Success")
	}, vaultURL, token
}

func waitForVault(vaultURL, token string) error {
	client := &http.Client{}
	req, err := http.NewRequest(http.MethodGet, vaultURL+"/v1/sys/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", token)

	for i := range 90 { // Try for a bit
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			return nil
		}
		if err != nil {
			fmt.Printf("vault not ready yet (iteration %d): %s\n", i, err)
		} else {
			fmt.Printf("vault not ready yet (iteration %d): status %d\n", i, resp.StatusCode)
			SilentClose(resp.Body)
		}
		time.Sleep(2 * time.Second)
	}

	return errors.Errorf("vault did not become ready in time")
}

func enableKVSecretEngine(vaultURL, token, path string) error {
	client := &http.Client{}
	reqBody := `{"type": "kv", "options": {"version": "1"}}`
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v1/sys/mounts/%s", vaultURL, path), strings.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer SilentClose(resp.Body)

	if resp.StatusCode != http.StatusNoContent {
		return errors.Errorf("failed to enable kv secret engine: %s", resp.Status)
	}

	return nil
}

type closer interface {
	Close() error
}

func SilentClose(c closer) {
	_ = c.Close()
}
