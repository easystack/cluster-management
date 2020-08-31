package k8s

import (
	"context"
	"fmt"
	"reflect"
	"strings"
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

const (
	// all nodes are ready and all components are healthy, we
	// think the cluster is healthy
	clusterHealthy = "Healthy"

	// if some node is notready or some component like apiserver,
	// controller-manager is unhealthy, we think this cluster
	// is unhealthy
	clusterWarning = "Warning"

	// if we can not connnect to cluster because some reason
	// we will set cluster cr status to DISCONNECTED
	clusterDisConnected = "Disconnected"

	// when controller call cluster apiserver if some error caused in request,
	// we will retry until maximum times exceeded, then controller
	// will change cluster cr status to clusterDisConnected
	maxRetryLimit = 3

	// master node key in node labels
	masterNodeKey = "node-role.kubernetes.io/master"

	// node compnents definition
	kubeApiserver = "kube-apiserver"

	kubeControllerManager = "kube-controller-manager"

	kubeScheduler = "kube-scheduler"

	etcd = "etcd"

	// cluster node role definition
	masterNode = "master"

	workerNode = "node"
)

type clusterStatusReaon []string

type KService struct {
	Token    *tokens.Token
	OSClient *openstack.OSService
	Cache    *clientCache
	sync.RWMutex
}

type clientCache struct {
	mu        sync.Mutex
	clientMap map[string]*kubernetes.Clientset
}

func (k *KService) CheckClusterInfo(ctx context.Context, cluster *ecnsv1.Cluster) (bool, error) {
	logger := utils.GetLoggerOrDie(ctx)

	client, _ := k.getK8sClient(ctx, cluster)
	var clusterNodes *v1.NodeList
	var systemPods *v1.PodList
	var reason clusterStatusReaon

	isDisconnected := false
	if err := k.Retry(ctx, func() error {
		//get cluster nodes
		nodes, err := client.CoreV1().Nodes().List(metav1.ListOptions{})
		if err != nil {
			logger.Error(err, "Failed to get nodes")
			return err
		} else {
			clusterNodes = nodes
		}
		//get kube-system pods
		pods, err := client.CoreV1().Pods("kube-system").List(metav1.ListOptions{})
		if err != nil {
			logger.Error(err, "Failed to get pods in kube-system namespace")
			return err
		} else {
			systemPods = pods
		}
		return nil
	}, maxRetryLimit); err != nil {
		message := fmt.Sprintf("Can not connect to the cluster through %s, the error is [%s]", cluster.Spec.Host, err.Error())
		reason = append(reason, message)
		isDisconnected = true
	}

	newClusterSpec, newClusterStatus := k.generateNewCluster(cluster, clusterNodes, systemPods, isDisconnected, &reason)

	if reflect.DeepEqual(newClusterSpec, cluster.Spec) && reflect.DeepEqual(newClusterStatus, cluster.Status) {
		return false, nil
	} else {
		cluster.Spec = newClusterSpec
		cluster.Status = newClusterStatus
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
		k.Lock()
		k.Token = token
		k.Unlock()
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
		Host:        cluster.Spec.Host,
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

func (k *KService) generateNewCluster(cluster *ecnsv1.Cluster, nodes *v1.NodeList, pods *v1.PodList, isDisconnected bool, reason *clusterStatusReaon) (ecnsv1.ClusterSpec, ecnsv1.ClusterStatus) {
	// if isDisconnected, means that some error occurred when request the cluster
	// we only update the cluster status to unknow and unhealthy
	if isDisconnected {
		var clusterStatus = ecnsv1.ClusterStatus{
			ClusterStatus:       clusterDisConnected,
			Nodes:               cluster.Status.Nodes,
			ClusterStatusReason: *reason,
			HasReconciledOnce:   cluster.Status.HasReconciledOnce,
		}
		return cluster.Spec, clusterStatus
	}

	// TODO(zhouxiao): now we do not consider eks cluster components,later we will remove it
	clusterType := cluster.Spec.Type

	newClusterNodes, newReason := k.getNodesStatus(nodes, pods, reason, clusterType)

	var newClusterStatus string
	if len(*newReason) == 0 {
		newClusterStatus = clusterHealthy
	} else {
		newClusterStatus = clusterWarning
	}

	var clusterStatus = ecnsv1.ClusterStatus{
		ClusterStatus:       newClusterStatus,
		Nodes:               newClusterNodes,
		ClusterStatusReason: *newReason,
		HasReconciledOnce:   cluster.Status.HasReconciledOnce,
	}

	node1 := nodes.Items[0]

	var clusterSpec = ecnsv1.ClusterSpec{
		Host:         cluster.Spec.Host,
		PublicVip:    cluster.Spec.PublicVip,
		Nodes:        len(nodes.Items),
		Version:      node1.Status.NodeInfo.KubeletVersion,
		Architecture: node1.Status.NodeInfo.Architecture,
		ClusterID:    cluster.Spec.ClusterID,
		Projects:     cluster.Spec.Projects,
		Type:         cluster.Spec.Type,
		Eks:          cluster.Spec.Eks,
	}

	return clusterSpec, clusterStatus
}

func (k *KService) getNodesStatus(nodes *v1.NodeList, pods *v1.PodList, csr *clusterStatusReaon, clusterType string) ([]ecnsv1.Node, *clusterStatusReaon) {
	var clusterNodes []ecnsv1.Node
	var nodeNameList []string

	// only master node has core components, so we only consider master node
	for _, node := range nodes.Items {
		if nodeRole := getNodeRole(&node); nodeRole == masterNode {
			nodeName := node.ObjectMeta.Name
			nodeNameList = append(nodeNameList, nodeName)
		}
	}

	// TODO(zhouxiao): now we do not consider eks cluster components,later we will remove it
	var nodesComponentStatusMap map[string]ecnsv1.ComponentStatus
	var reason *clusterStatusReaon

	if clusterType != "EKS" {
		nodesComponentStatusMap, reason = getNodesComponentStatusAndGenerateClusterStatusReaon(pods, nodeNameList, csr)
		csr = reason
	}

	for _, node := range nodes.Items {
		nodeName := node.ObjectMeta.Name
		nodeRole := getNodeRole(&node)
		nodeConditon := getNodeCondition(&node)

		if nodeConditon != "Ready" {
			message := fmt.Sprintf("[%s] Node %s Status is NotReady", nodeName, nodeName)
			*csr = append(*csr, message)
		}

		clusterNode := ecnsv1.Node{
			Name:   nodeName,
			Role:   nodeRole,
			Status: nodeConditon,
		}

		// TODO(zhouxiao): now we do not consider eks cluster components,later we will remove it
		if nodeRole == masterNode && clusterType != "EKS" {
			clusterNode.Component = nodesComponentStatusMap[nodeName]
		}

		clusterNodes = append(clusterNodes, clusterNode)
	}
	return clusterNodes, csr
}

func getNodeRole(node *v1.Node) string {
	for label, _ := range node.ObjectMeta.Labels {
		if label == masterNodeKey {
			return masterNode
		}
	}
	return workerNode
}

func getNodeCondition(node *v1.Node) string {
	for i := range node.Status.Conditions {
		cond := node.Status.Conditions[i]
		if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
			return "Ready"
		}
	}
	return "NotReady"
}

func getNodesComponentStatusAndGenerateClusterStatusReaon(pods *v1.PodList, nodesName []string, reason *clusterStatusReaon) (map[string]ecnsv1.ComponentStatus, *clusterStatusReaon) {
	nodesComponentStatusMap := make(map[string]ecnsv1.ComponentStatus)

	nodesComponentMap := getNodesComponentMap(pods, nodesName)

	for nodeName, components := range nodesComponentMap {
		cs := ecnsv1.ComponentStatus{}
		for component, condition := range components {
			// TODO(zhouxiao): Now we do not consider etcd
			if component != etcd {
				if condition != "Healthy" {
					message := fmt.Sprintf("[%s]:Node %s Component %s Status is %s", nodeName, nodeName, component, condition)
					*reason = append(*reason, message)
				}

				switch component {
				case kubeApiserver:
					cs.ApiserverStatus = condition
				case kubeControllerManager:
					cs.ControllerManagerStatus = condition
				case kubeScheduler:
					cs.SchedulerStatus = condition
				default:
					continue
				}
			}
		}
		nodesComponentStatusMap[nodeName] = cs
	}
	return nodesComponentStatusMap, reason
}

func getNodesComponentMap(pods *v1.PodList, nodesName []string) map[string]map[string]string {
	nodesComponentMap := make(map[string]map[string]string)
	for _, node := range nodesName {
		nodesComponentMap[node] = map[string]string{
			kubeApiserver:         "UnKnow",
			kubeControllerManager: "UnKnow",
			kubeScheduler:         "UnKnow",
			etcd:                  "UnKnow",
		}
	}

	for _, pod := range pods.Items {
		podName := pod.ObjectMeta.Name
		podScheduledNodeName := pod.Spec.NodeName
		podPhase := pod.Status.Phase

		switch {
		case strings.Contains(podName, kubeApiserver):
			if _, ok := nodesComponentMap[podScheduledNodeName]; ok {
				if podPhase == v1.PodRunning {
					nodesComponentMap[podScheduledNodeName][kubeApiserver] = "Healthy"
				} else {
					nodesComponentMap[podScheduledNodeName][kubeApiserver] = "UnHealthy"
				}
			}
		case strings.Contains(podName, kubeControllerManager):
			if _, ok := nodesComponentMap[podScheduledNodeName]; ok {
				if podPhase == v1.PodRunning {
					nodesComponentMap[podScheduledNodeName][kubeControllerManager] = "Healthy"
				} else {
					nodesComponentMap[podScheduledNodeName][kubeControllerManager] = "UnHealthy"
				}
			}
		case strings.Contains(podName, kubeScheduler):
			if _, ok := nodesComponentMap[podScheduledNodeName]; ok {
				if podPhase == v1.PodRunning {
					nodesComponentMap[podScheduledNodeName][kubeScheduler] = "Healthy"
				} else {
					nodesComponentMap[podScheduledNodeName][kubeScheduler] = "UnHealthy"
				}
			}
		default:
			continue
		}
	}
	return nodesComponentMap
}

func (k *KService) Retry(ctx context.Context, fn func() error, attempts int) error {
	logger := utils.GetLoggerOrDie(ctx)
	if err := fn(); err != nil {
		if attempts--; attempts > 0 {
			logger.Info("Retry Error !", "ErrorIs", err.Error())
			time.Sleep(time.Duration(500) * time.Millisecond)
			return k.Retry(ctx, fn, attempts)
		}
		return err
	}
	return nil
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
