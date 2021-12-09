package k8s

import (
	"context"
	"sync"
	"time"

	v1 "github.com/cluster-management/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	cli "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultTimeout = 10 * time.Second
	// master node key in node labels
	NodeLabelKeyMaster = "node-role.kubernetes.io/master"
	NodeLabelNodeGroup = "node.es.io/nodegroup"
)

type getClientFn func() (cli.Client, error)

type Client struct {
	ctx context.Context

	host string
	// had sync or not
	synced bool

	//the last error on apiserver call
	lasterr error
	fn      getClientFn

	cli cli.Client

	//key: nodename
	nodes map[string]*v1.Node

	mu sync.RWMutex
}

func newClient(fn getClientFn, host string) *Client {
	if fn == nil {
		panic("not define get client func")
	}

	return &Client{
		ctx:   context.Background(),
		host:  host,
		fn:    fn,
		cli:   nil,
		nodes: make(map[string]*v1.Node),
		mu:    sync.RWMutex{},
	}
}

func (c *Client) update() (rerr error) {
	var (
		nodes  corev1.NodeList
		err    error
		hadnew bool
	)
	defer func() {
		c.synced = true
		c.lasterr = rerr
	}()

	if c.cli == nil {
		c.cli, err = c.fn()
		if err != nil {
			klog.Errorf("new client failed:%s", err)
			return err
		}
	}
	cctx, canclefn := context.WithTimeout(c.ctx, defaultTimeout)
	defer canclefn()
	for {
		err = c.cli.List(cctx, &nodes)
		if err != nil {
			//retry on any error, even if unauthorized
			klog.Errorf("list node failed: %s", err)
			if hadnew {
				return err
			}
			hadnew = true
			c.cli, err = c.fn()
			if err != nil {
				klog.Errorf("try new client failed:%s", err)
				return err
			}
		} else {
			break
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range nodes.Items {
		tmpnode := &v1.Node{}
		tmpnode.Name = node.Name
		tmpnode.Arch = node.Status.NodeInfo.Architecture
		tmpnode.Version = node.Status.NodeInfo.KubeletVersion
		tmpnode.Capacity = node.Status.Capacity
		tmpnode.NodeGroup = node.Labels[NodeLabelNodeGroup]

		if _, ok := node.Labels[NodeLabelKeyMaster]; ok {
			tmpnode.Role = v1.NodeRoleMaster
		} else {
			tmpnode.Role = v1.NodeRoleWorker
		}

		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				if cond.Status == corev1.ConditionTrue {
					tmpnode.Status = v1.NodeStatReady
				} else {
					tmpnode.Status = v1.NodeStatNotReady
				}
				break
			}
		}

		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP {
				tmpnode.InternalIP = address.Address
			}
			if address.Type == corev1.NodeExternalIP {
				tmpnode.ExternalIP = address.Address
			}
		}

		_, ok := c.nodes[tmpnode.Name]
		if !ok {
			c.nodes[tmpnode.Name] = tmpnode.DeepCopy()
		} else {
			tmpnode.DeepCopyInto(c.nodes[tmpnode.Name])
		}
	}
	for _, node := range nodes.Items {
		if _, ok := c.nodes[node.Name]; !ok {
			delete(c.nodes, node.Name)
		}
	}
	return nil
}

func (c *Client) HadSyncd() bool {
	return c.synced
}

func (c *Client) Nodes() ([]*v1.Node, error) {
	var (
		nodes = make([]*v1.Node, len(c.nodes))
		i     int
	)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, v := range c.nodes {
		nodes[i] = v.DeepCopy()
		i++
	}
	return nodes, c.lasterr
}
