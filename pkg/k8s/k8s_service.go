package k8s

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/cluster-management/pkg/openstack"
	"github.com/cluster-management/pkg/utils"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"

	ecnsv1 "github.com/cluster-management/pkg/api/v1"
)

type KService struct {
	Host     string
	Token    *tokens.Token
	OSClient *openstack.OSService
	Cache    *clientCache
}

type clientCache struct {
	mu        sync.Mutex
	clientMap map[string]*kubernetes.Clientset
}

func (k *KService) CheckClusterInfo(ctx context.Context, cluster *ecnsv1.Cluster) (bool, error) {
	logger := utils.GetLoggerOrDie(ctx)

	client, _ := k.getK8sClient(ctx, cluster)
	nodes, err := client.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		logger.Error(err, "Failed to get nodes")
		return false, err
	}

	newClusterSpec := k.generateNewClusterSpec(cluster, nodes)

	if reflect.DeepEqual(newClusterSpec, cluster.Spec) {
		return false, nil
	} else {
		cluster.Spec = newClusterSpec
		return true, nil
	}
}

// ensure get correct k8s client very time because that cluster host maybe change every time
func (k *KService) getK8sClient(ctx context.Context, cluster *ecnsv1.Cluster) (*kubernetes.Clientset, error) {
	logger := utils.GetLoggerOrDie(ctx)

	if k.Cache == nil {
		logger.Info("Init cache for k8s clients")
		k.Cache = &clientCache{clientMap: make(map[string]*kubernetes.Clientset)}
	}

	key := cluster.Spec.Host
	cs, ok := k.Cache.get(key)
	if k.Token == nil || k.Token.ExpiresAt.Before(time.Now()) {
		logger.Info("Token is nil or is expired, need to renew")
		token, _ := k.OSClient.GetKeystoneToken(ctx)
		k.Token = token
		cs, _ := k.newK8sClient(ctx, cluster)
		k.Cache.set(key, cs)
	} else if !ok {
		logger.Info("GetClusterInfo: no k8s client, need to create new one")
		cs, _ = k.newK8sClient(ctx, cluster)
		k.Cache.set(key, cs)
	}

	return cs, nil
}

func (k *KService) newK8sClient(ctx context.Context, cluster *ecnsv1.Cluster) (*kubernetes.Clientset, error) {
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

func (k *KService) AssignClusterToProjects(ctx context.Context, cluster *ecnsv1.Cluster, projects []string) error {
	logger := utils.GetLoggerOrDie(ctx)

	logger.Info("Assign Cluster to Projects", "Assign", projects)

	// create NameSpace
	if err := k.createNameSpace(ctx, cluster, projects); err != nil {
		return err
	}

	//create Pod Security Policy

	//create Network Policy

	return nil
}

func (k *KService) UnAssignClusterToProjects(ctx context.Context, cluster *ecnsv1.Cluster, projects []string) error {
	logger := utils.GetLoggerOrDie(ctx)

	logger.Info("UnAssign Cluster from Projects", "UnAssign", projects)
	// delete NameSpace
	if err := k.deleteNameSpace(ctx, cluster, projects); err != nil {
		return err
	}

	//create Pod Security Policy

	//create Network Policy

	return nil
}

func (k *KService) createNameSpace(ctx context.Context, cluster *ecnsv1.Cluster, projects []string) error {
	logger := utils.GetLoggerOrDie(ctx)

	client, _ := k.getK8sClient(ctx, cluster)
	for _, p := range projects {
		ns := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: p,
			},
		}
		_, err := client.CoreV1().Namespaces().Create(ns)
		if err != nil && !apierrs.IsAlreadyExists(err) {
			logger.Error(err, "Failed to create namespace")
			return err
		}
	}
	return nil
}

func (k *KService) deleteNameSpace(ctx context.Context, cluster *ecnsv1.Cluster, projects []string) error {
	logger := utils.GetLoggerOrDie(ctx)

	client, _ := k.getK8sClient(ctx, cluster)
	for _, p := range projects {
		err := client.CoreV1().Namespaces().Delete(p, &metav1.DeleteOptions{})
		if err != nil && !apierrs.IsNotFound(err) {
			logger.Error(err, "Failed to delete namespace")
			return err
		}
	}

	return nil
}

func (k *KService) generateNewClusterSpec(cluster *ecnsv1.Cluster, nodes *v1.NodeList) ecnsv1.ClusterSpec {
	var node1 = nodes.Items[0]
	var status = "Ready"

	if !k.calculateClusterStatus(nodes) {
		status = "NotReady"
	}

	var clusterSpec = ecnsv1.ClusterSpec{
		Host:         cluster.Spec.Host,
		Nodes:        len(nodes.Items),
		Version:      node1.Status.NodeInfo.KubeletVersion,
		Architecture: node1.Status.NodeInfo.Architecture,
		Status:       status,
		ClusterID:    cluster.Spec.ClusterID,
		Projects:     cluster.Spec.Projects,
		Type:         cluster.Spec.Type,
		Eks:          cluster.Spec.Eks,
	}

	return clusterSpec
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

// get a cached client
func (s *clientCache) get(key string) (*kubernetes.Clientset, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cluster, ok := s.clientMap[key]
	return cluster, ok
}

// set a client cache
func (s *clientCache) set(key string, client *kubernetes.Clientset) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clientMap[key] = client
}

// delete a cached client
func (s *clientCache) delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clientMap, key)
}
