package license

import (
	"fmt"
	v1 "github.com/cluster-management/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"reflect"
	"testing"
)

func makeConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		Data: map[string]string{
			"license": "CPU: \"40\"\nCPU_flavor: \"4\"\nClusters: \"10\"\nMemory: \"819200\"\nMemory_flavor: \"8192\"\nNodes: \"100\"\n",
		},
	}
}

func makeLicense() *License {
	return &License{
		CPU:          "40",
		CPUFlavor:    "4",
		Clusters:     "10",
		Memory:       "819200",
		MemoryFlavor: "8192",
		Nodes:        "100",
	}
}

func makeNodes(cpu, memory string) []*v1.Node {
	return []*v1.Node{
		{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
			},
		},
	}
}

func TestReadLicense(t *testing.T) {
	cm := makeConfigMap()
	targetLic := makeLicense()

	currentLic, err := ReadFromConfigMap(cm)
	if err != nil {
		t.Errorf("Test failed: %v", err)
	}
	if !reflect.DeepEqual(currentLic, targetLic) {
		t.Errorf("Test failed: Current license is not equal to the target one")
	}
}

func TestCheckLicense(t *testing.T) {
	lic := makeLicense()
	legalNodes := makeNodes("4", "7999924Ki")
	illegalNodes1 := makeNodes("5", "7999924Ki")
	illegalNodes2 := makeNodes("3", fmt.Sprintf("%dKi", 8192<<20+1))

	isIllegal, clusterStatusReason := lic.CheckNodes(legalNodes)
	fmt.Printf("%v\n", clusterStatusReason)
	if isIllegal {
		t.Errorf("Test failed")
	}

	isIllegal, clusterStatusReason = lic.CheckNodes(illegalNodes1)
	fmt.Printf("%v\n", clusterStatusReason)
	if !isIllegal {
		t.Errorf("Test failed")
	}

	isIllegal, clusterStatusReason = lic.CheckNodes(illegalNodes2)
	fmt.Printf("%v\n", clusterStatusReason)
	if !isIllegal {
		t.Errorf("Test failed")
	}
}
