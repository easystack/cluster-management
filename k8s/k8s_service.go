package k8s

import (
	"context"
	"fmt"
	"github.com/cluster-management/utils"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"

	eosv1 "github.com/cluster-management/api/v1"
)

type KService struct {
	Host string
	Token *tokens.Token
	Client *kubernetes.Clientset
}

func (k *KService) NewK8sClient(ctx context.Context) (*kubernetes.Clientset, error) {
	logger := utils.GetLoggerOrDie(ctx)

	config := rest.Config{
		Host:        k.Host,
		BearerToken: k.Token.ID,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}
	cs, err := kubernetes.NewForConfig(&config)
	if err != nil {
		logger.Error(err, "Failed to create k8s client")
		return nil, err
	}
	return cs, nil
}

func (k *KService) GetClusterInfo(ctx context.Context, cluster *eosv1.EosCluster) error {
	logger := utils.GetLoggerOrDie(ctx)

	client := k.Client
	if client == nil {
		logger.Info("k8s client is nil, need to create new one")
		client, _ = k.NewK8sClient(ctx)
	}

	nodes, err := client.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		logger.Error(err, "Failed to get nodes")
		return err
	}

	return k.generateNewCluster(ctx, cluster, nodes)
}

func (k *KService) generateNewCluster(ctx context.Context, cluster *eosv1.EosCluster, nodes *v1.NodeList) error {
	var node1 = nodes.Items[0]
	var status = "Ready"

	if ! k.calculateClusterStatus(nodes){
		status = "NotReady"
	}

	var clusterSpec = eosv1.EosClusterSpec{
		Host: cluster.Spec.Host,
		Nodes: len(nodes.Items),
		Version: node1.Status.NodeInfo.KubeletVersion,
		Architecture: node1.Status.NodeInfo.Architecture,
		Status: status,
	}

	cluster.Spec = clusterSpec

	return nil
}

func (k *KService) calculateClusterStatus(nodes *v1.NodeList) bool{

	// calculate cluster status from nodes status
	// Cluster is ready if all nodes are ready
	for i := range nodes.Items {
		ready, _ :=k.getNodeStatus(&nodes.Items[i])
		if !ready {
			return false
		}
	}

	return true
}

func (k *KService) getNodeStatus(node *v1.Node) (bool, error) {

	for i := range node.Status.Conditions {
		cond := node.Status.Conditions[i]
		if cond.Type == v1.NodeReady{
			return cond.Status == v1.ConditionTrue, nil
		}
	}

	return false, fmt.Errorf("failed to get node Ready status")
}