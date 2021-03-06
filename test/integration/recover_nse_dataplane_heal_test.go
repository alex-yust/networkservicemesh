// +build recover

package nsmd_integration_tests

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"

	"github.com/networkservicemesh/networkservicemesh/test/kubetest"
	"github.com/networkservicemesh/networkservicemesh/test/kubetest/pods"
)

func TestDataplaneHealLocal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skip, please run without -short")
		return
	}

	g := NewWithT(t)

	testDataplaneHeal(t, 0, 1, kubetest.DefaultTestingPodFixture(g))
}

func TestDataplaneHealLocalMemif(t *testing.T) {
	if testing.Short() {
		t.Skip("Skip, please run without -short")
		return
	}

	g := NewWithT(t)

	testDataplaneHeal(t, 0, 1, kubetest.VppAgentTestingPodFixture(g))
}

func TestDataplaneHealMultiNodesLocal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skip, please run without -short")
		return
	}

	g := NewWithT(t)

	testDataplaneHeal(t, 0, 2, kubetest.HealTestingPodFixture(g))
}
func TestDataplaneHealMultiNodesRemote(t *testing.T) {
	if testing.Short() {
		t.Skip("Skip, please run without -short")
		return
	}

	g := NewWithT(t)

	testDataplaneHeal(t, 1, 2, kubetest.HealTestingPodFixture(g))
}

/**
If passed 1 both will be on same node, if not on different.
*/
func testDataplaneHeal(t *testing.T, killDataplaneIndex, nodesCount int, fixture kubetest.TestingPodFixture) {
	g := NewWithT(t)

	g.Expect(nodesCount > 0).Should(BeTrue())
	g.Expect(killDataplaneIndex >= 0 && killDataplaneIndex < nodesCount).Should(BeTrue())
	k8s, err := kubetest.NewK8s(g, true)
	defer k8s.Cleanup()
	g.Expect(err).To(BeNil())

	// Deploy open tracing to see what happening.
	nodes_setup, err := kubetest.SetupNodes(k8s, nodesCount, defaultTimeout)
	g.Expect(err).To(BeNil())
	defer kubetest.MakeLogsSnapshot(k8s, t)
	// Run ICMP on latest node
	fixture.DeployNse(k8s, nodes_setup[nodesCount-1].Node, "icmp-responder-nse-1", defaultTimeout)

	nscPodNode := fixture.DeployNsc(k8s, nodes_setup[0].Node, "nsc-1", defaultTimeout)
	fixture.CheckNsc(k8s, nscPodNode)

	logrus.Infof("Delete Selected dataplane")
	k8s.DeletePods(nodes_setup[killDataplaneIndex].Dataplane)

	logrus.Infof("Wait NSMD is waiting for dataplane recovery")
	k8s.WaitLogsContains(nodes_setup[killDataplaneIndex].Nsmd, "nsmd", "Waiting for Dataplane to recovery...", defaultTimeout)
	// Now are are in dataplane dead state, and in Heal procedure waiting for dataplane.
	dpName := fmt.Sprintf("nsmd-dataplane-recovered-%d", killDataplaneIndex)

	logrus.Infof("Starting recovered dataplane...")
	startTime := time.Now()
	nodes_setup[killDataplaneIndex].Dataplane = k8s.CreatePod(pods.ForwardingPlane(dpName, nodes_setup[killDataplaneIndex].Node, k8s.GetForwardingPlane()))
	logrus.Printf("Started new Dataplane: %v on node %s", time.Since(startTime), nodes_setup[killDataplaneIndex].Node.Name)

	// Check NSMd goint into HEAL state.

	logrus.Infof("Waiting for connection recovery...")
	if nodesCount > 1 && killDataplaneIndex != 0 {
		k8s.WaitLogsContains(nodes_setup[nodesCount-1].Nsmd, "nsmd", "Healing will be continued on source side...", defaultTimeout)
		k8s.WaitLogsContains(nodes_setup[0].Nsmd, "nsmd", "Heal: Connection recovered:", defaultTimeout)
	} else {
		k8s.WaitLogsContains(nodes_setup[killDataplaneIndex].Nsmd, "nsmd", "Heal: Connection recovered:", defaultTimeout)
	}
	logrus.Infof("Waiting for connection recovery Done...")
	fixture.CheckNsc(k8s, nscPodNode)
}
