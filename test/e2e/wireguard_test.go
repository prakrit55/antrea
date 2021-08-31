// Copyright 2021 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"antrea.io/antrea/pkg/agent/config"
	"antrea.io/antrea/pkg/apis"
)

// TestWireGuard checks that Pod traffic across two Nodes over the WireGuard tunnel  by creating
// multiple Pods across distinct Nodes and having them ping each other. It will also verify that
// the handshake was established when the wg command line is available.
func TestWireGuard(t *testing.T) {
	skipIfNumNodesLessThan(t, 2)
	skipIfHasWindowsNodes(t)
	providerIsKind := testOptions.providerName == "kind"
	if !providerIsKind {
		for _, node := range clusterInfo.nodes {
			skipIfMissingKernelModule(t, node.name, []string{"wireguard"})
		}
	}
	data, err := setupTest(t)
	skipIfEncapModeIsNot(t, data, config.TrafficEncapModeEncap)

	if err != nil {
		t.Fatalf("Error when setting up test: %v", err)
	}
	defer teardownTest(t, data)

	if !providerIsKind {
		ac := []configChange{
			{"trafficEncryptionMode", "wireguard", false},
		}
		if err := data.mutateAntreaConfigMap(nil, ac, false, true); err != nil {
			t.Fatalf("Failed to enable WireGuard tunnel: %v", err)
		}
		defer func() {
			ac = []configChange{
				{"trafficEncryptionMode", "none", false},
			}
			if err := data.mutateAntreaConfigMap(nil, ac, false, true); err != nil {
				t.Fatalf("Failed to disable WireGuard tunnel: %v", err)
			}
		}()
	} else {
		data.redeployAntrea(t, deployAntreaWireGuardGo)
		defer data.redeployAntrea(t, deployAntreaDefault)
	}

	t.Run("testWireGuardTunnelConnectivity", func(t *testing.T) { testWireGuardTunnelConnectivity(t, data) })
}

func (data *TestData) getWireGuardPeerEndpointsWithHandshake(nodeName string) ([]string, error) {
	var peerEndpoints []string
	antreaPodName, err := data.getAntreaPodOnNode(nodeName)
	if err != nil {
		return peerEndpoints, err
	}
	cmd := []string{"wg"}
	stdout, stderr, err := data.runCommandFromPod(antreaNamespace, antreaPodName, "wireguard", cmd)
	if err != nil {
		return peerEndpoints, fmt.Errorf("error when running 'wg' on '%s': %v - stdout: %s - stderr: %s", nodeName, err, stdout, stderr)
	}
	peerConfigs := strings.Split(stdout, "\n\n")
	if len(peerConfigs) < 1 {
		return peerEndpoints, fmt.Errorf("invalid 'wg' output on '%s': %v - stdout: %s - stderr: %s", nodeName, err, stdout, stderr)
	}

	for _, p := range peerConfigs[1:] {
		lines := strings.Split(p, "\n")
		if len(lines) < 2 {
			return peerEndpoints, fmt.Errorf("invalid WireGuard peer config output - %s", p)
		}
		peerEndpoint := strings.TrimPrefix(strings.TrimSpace(lines[1]), "endpoint: ")
		for _, l := range lines {
			if strings.Contains(l, "latest handshake") {
				peerEndpoints = append(peerEndpoints, peerEndpoint)
				break
			}
		}
	}
	return peerEndpoints, nil
}

func testWireGuardTunnelConnectivity(t *testing.T, data *TestData) {
	podInfos, deletePods := createPodsOnDifferentNodes(t, data, "differentnodes")
	defer deletePods()
	numPods := 2
	data.runPingMesh(t, podInfos[:numPods], agnhostContainerName)
	// wg command is only available in WireGuard sidecar container.
	if testOptions.providerName == "kind" {
		nodeName0 := podInfos[0].nodeName
		nodeName1 := podInfos[1].nodeName
		endpoints, err := data.getWireGuardPeerEndpointsWithHandshake(nodeName0)
		require.NoError(t, err)
		t.Logf("Found peer endpoints %v with handshake established for Node '%s'", endpoints, nodeName0)
		var nodeIP string
		for _, n := range clusterInfo.nodes {
			if n.name == nodeName1 {
				nodeIP = n.ip
				break
			}
		}
		assert.Contains(t, endpoints, fmt.Sprintf("%s:%d", nodeIP, apis.WireGuardListenPort))
	}
}