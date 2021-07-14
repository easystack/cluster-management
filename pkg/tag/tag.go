package tag

import (
	"context"
	"encoding/json"
	"fmt"

	v1 "github.com/cluster-management/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/asmcos/requests"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"time"

	"k8s.io/klog/v2"
)

var (
	clusterGVR = schema.GroupVersionResource{
		Group:    "ecns.easystack.com",
		Version:  "v1",
		Resource: "clusters",
	}
)

const (
	EKS_NAMESPACE = "eks"
	EKS_TYPE      = "EKS"
)

type TagMgr struct {
	du        time.Duration
	stopch    chan struct{}
	ekstagurl string
}

type EOSClient struct {
	client dynamic.Interface
}

func (tm *TagMgr) NewEOSclient(kubeconfig string) (*EOSClient, error) {
	var config *rest.Config
	var err error

	if kubeconfig == "" {
		config, err = rest.InClusterConfig()
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	if err != nil {
		return nil, err
	}

	client, err := dynamic.NewForConfig(config)

	if err != nil {
		return nil, err
	}

	return &EOSClient{client: client}, nil
}

func NewTagMgr(du time.Duration, createurl string) *TagMgr {
	tm := &TagMgr{
		du:        du,
		ekstagurl: createurl,
		stopch:    make(chan struct{}),
	}
	return tm
}

func (tm *TagMgr) Stop() {
	close(tm.stopch)
}

func (tm *TagMgr) Run(ctx context.Context) {
	go tm.loop(ctx, tm.du)
}

func (tm *TagMgr) tagHandler(clust *v1.Cluster) error {
	var (
		spec   = &clust.Spec
		status = &clust.Status
		labels = &clust.ObjectMeta
	)
	req := requests.Requests()
	project_id := ""
	cluster_name := ""
	klog.Infof("Auto create tag of cluster %v", labels.Labels)
	if len(spec.Projects) > 0 {
		project_id = spec.Projects[0]
	}
	for k, v := range labels.Labels {
		if k == "clustername" {
			cluster_name = v
		}
	}
	p := requests.Params{
		"project_id":   project_id,
		"cluster_id":   spec.ClusterID,
		"status":       string(status.ClusterStatus),
		"stack_id":     spec.Eks.EksStackID,
		"cluster_name": cluster_name,
	}
	klog.V(4).Infof("request prarms is %v", p)
	_, err := req.Get(tm.ekstagurl, p)
	if err != nil {
		return fmt.Errorf("get req of cluster_id: %s failed", spec.ClusterID)
	}
	return nil
}

func (tm *TagMgr) loop(ctx context.Context, du time.Duration) {
	cli, err := tm.NewEOSclient("")
	if err != nil {
		klog.Errorf("Init eos client error for tag create")
	}
	for {
		select {
		case <-ctx.Done():
			klog.Errorf("ctx failed:%v", ctx.Err())
			return
		case <-tm.stopch:
			klog.Infof("TagManager receive stop signal")
			return
		case <-time.NewTimer(du).C:
			klog.V(4).Infof("start send tag create signal loop at %v", time.Now().Format(time.RFC3339))
			list, err := cli.client.Resource(clusterGVR).Namespace("eks").List(context.Background(), metav1.ListOptions{})
			if err != nil {
				klog.Errorf("Get eos ecns.easystack.com rs failed:%v", err)
			}

			for _, c := range list.Items {
				jsonBytes, err := c.MarshalJSON()
				if err != nil {
					klog.Errorf("MarshalJson failed while create tag.")
				}
				eksCluster := v1.Cluster{}
				err = json.Unmarshal(jsonBytes, &eksCluster)
				if err != nil {
					klog.Errorf("Unmarshal failed while create eksCluster obj.")
				}
				if eksCluster.Spec.Type != EKS_TYPE {
					klog.V(4).Infof("Is not eks cluster data, skip")
					continue
				} else {
					err := tm.tagHandler(&eksCluster)
					if err != nil {
						klog.Errorf("Cluster %v Send rest to eks-dashboard-api error.", eksCluster.ObjectMeta.Labels)
					}
				}
			}

			klog.V(4).Infof("end send tag create signal loop at %v", time.Now().Format(time.RFC3339))
		}
	}
}
