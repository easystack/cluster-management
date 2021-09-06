package license

import (
	"fmt"
	v1 "github.com/cluster-management/pkg/api/v1"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"strconv"
)

const (
	ConfigMapLicense = "magnum-etc"
)

const (
	LicenseKey = "license"
)

const (
	limitsUnlimited = "Unlimited"
)

// License limit the resource quantity of a cluster which defined in ConfigMap "magnum-etc"
// e.g.
// license: |
//  CPU: "40"
//  CPU_flavor: "4"
//  Clusters: "10"
//  Memory: "819200"
//  Memory_flavor: "8192"
//  Nodes: "100"
type License struct {
	CPU          string `yaml:"CPU"`
	// CPU Flavor, in cores.
	CPUFlavor    string `yaml:"CPU_flavor"`
	Clusters     string `yaml:"Clusters"`
	Memory       string `yaml:"Memory"`
	// Memory Flavor, in Mi, must be number.
	MemoryFlavor string `yaml:"Memory_flavor"`
	Nodes        string `yaml:"Nodes"`
}

func ReadFromConfigMap(configMap *corev1.ConfigMap) (*License, error) {
	var license = &License{}

	rawConfig := configMap.Data[LicenseKey]
	if rawConfig == "" {
		return nil, fmt.Errorf("license is not found in ConfigMap")
	}
	err := yaml.Unmarshal([]byte(rawConfig), license)
	if err != nil {
		return nil, err
	}
	return license, nil
}

func (l *License) CheckNodes(nodes []*v1.Node) (bool, []string) {
	var isIllegal bool
	var clusterStatusReason []string

	check := func(item string, fn func(limit int64)) {
		if item == limitsUnlimited {
			return
		}
		limit, err := strconv.ParseInt(item, 10, 64)
		if err != nil {
			clusterStatusReason = append(clusterStatusReason, fmt.Sprintf("%v", err))
			isIllegal = true
			return
		}
		fn(limit)
	}

	for _, node := range nodes {
		capacity := node.Capacity
		// Check each node CPU_flavor is illegal
		check(l.CPUFlavor, func(limit int64) {
			current := capacity.Cpu().Value()
			if current > limit {
				isIllegal = true
				clusterStatusReason = append(clusterStatusReason, fmt.Sprintf("Node: %s CPU Flavor: %d exceeds license limit", node.Name, current))
			}
		})
		// Check each node Memory_flavor is illegal
		check(l.MemoryFlavor, func(limit int64) {
			current := capacity.Memory().Value()
			if current > limit<<20 {
				isIllegal = true
				clusterStatusReason = append(clusterStatusReason, fmt.Sprintf("Node: %s Memory Flavor: %d exceeds license limit", node.Name, current))
			}
		})
		// add more check if need
	}

	return isIllegal, clusterStatusReason
}
