package k8s

import (
	"context"
	"fmt"
	"github.com/cluster-management/openstack"
	"github.com/cluster-management/utils"
	"time"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"

	eosv1 "github.com/cluster-management/api/v1"
)

type KService struct {
	Host   string
	Token  *tokens.Token
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

func (k *KService) GetClusterInfo(ctx context.Context, cluster *eosv1.EosCluster, osService *openstack.OSService) error {
	logger := utils.GetLoggerOrDie(ctx)

	if k.Token == nil || k.Token.ExpiresAt.Before(time.Now()) {
		logger.Info("Token is nil or is expired, need to renew")
		token, _ := osService.GetKeystoneToken(ctx)
		k.Token = token
	}

	if k.Client == nil {
		logger.Info("GetClusterInfo: no k8s client, need to create new one")
		k.Client, _ = k.NewK8sClient(ctx)
	}

	nodes, err := k.Client.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		logger.Error(err, "Failed to get nodes")
		return err
	}

	return k.generateNewCluster(ctx, cluster, nodes)
}

func (k *KService) AssignClusterToProjects(ctx context.Context, cluster *eosv1.EosCluster, projects []string) error {

	// create NameSpace
	k.createNameSpace(ctx, cluster, projects)

	//create Pod Security Policy

	//create Network Policy

	return nil
}

func (k *KService) UnAssignClusterToProjects(ctx context.Context, cluster *eosv1.EosCluster, projects []string) error {

	// delete NameSpace

	//create Pod Security Policy

	//create Network Policy

	return nil
}

func (k *KService) createNameSpace(ctx context.Context, cluster *eosv1.EosCluster, projects []string) error {
	logger := utils.GetLoggerOrDie(ctx)

	for _, p := range projects {
		ns := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: p,
			},
		}
		_, err := k.Client.CoreV1().Namespaces().Create(ns)
		if err != nil {
			logger.Error(err, "Failed to create namespace")
			return err
		}
	}

	return nil
}

func (k *KService) generateNewCluster(ctx context.Context, cluster *eosv1.EosCluster, nodes *v1.NodeList) error {
	var node1 = nodes.Items[0]
	var status = "Ready"

	if !k.calculateClusterStatus(nodes) {
		status = "NotReady"
	}

	var clusterSpec = eosv1.EosClusterSpec{
		Host:         cluster.Spec.Host,
		Nodes:        len(nodes.Items),
		Version:      node1.Status.NodeInfo.KubeletVersion,
		Architecture: node1.Status.NodeInfo.Architecture,
		Status:       status,
		Projects:     cluster.Spec.Projects,
	}

	cluster.Spec = clusterSpec

	return nil
}

func (k *KService) calculateClusterStatus(nodes *v1.NodeList) bool {

	// calculate cluster status from nodes status
	// Cluster is ready if all nodes are ready
	for i := range nodes.Items {
		ready, _ := k.getNodeStatus(&nodes.Items[i])
		if !ready {
			return false
		}
	}

	return true
}

func (k *KService) getNodeStatus(node *v1.Node) (bool, error) {

	for i := range node.Status.Conditions {
		cond := node.Status.Conditions[i]
		if cond.Type == v1.NodeReady {
			return cond.Status == v1.ConditionTrue, nil
		}
	}

	return false, fmt.Errorf("failed to get node Ready status")
}
