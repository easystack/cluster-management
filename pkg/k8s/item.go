package k8s

import (
	"context"
	backoff2 "github.com/cenkalti/backoff/v4"
	v1 "github.com/cluster-management/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	cli "sigs.k8s.io/controller-runtime/pkg/client"
	"sync"
	"time"
)

const (
	//TODO should not little than 2,
	// when unauth, will auth and try again
	defaultRetry = 2
	// master node key in node labels
	NodeLabelKeyMaster = "node-role.kubernetes.io/master"
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

	backoff backoff2.BackOff

	mu sync.RWMutex
}

func newClient(fn getClientFn, host string) *Client {
	if fn == nil {
		panic("not define get client func")
	}
	backoff := backoff2.WithMaxRetries(backoff2.NewConstantBackOff(time.Millisecond*100), defaultRetry)

	return &Client{
		ctx:     context.Background(),
		host:    host,
		fn:      fn,
		cli:     nil,
		backoff: backoff,
		nodes:   make(map[string]*v1.Node),
		mu:      sync.RWMutex{},
	}
}

func (c *Client) update() (rerr error) {
	var (
		nodes   corev1.NodeList
		err     error
		tmpnode = &v1.Node{}
	)
	defer func() {
		c.synced = true
		c.lasterr = rerr
	}()

	if c.cli == nil {
		c.cli, err = c.fn()
		if err != nil {
			return err
		}
	}
	klog.V(3).Infof("start list nodes on host:%v", c.host)
	for {
		err = c.cli.List(c.ctx, &nodes)
		if err != nil {
			//retry on any error, even if unauthorized
			if !apierrs.IsUnauthorized(err) {
				return err
			}
			klog.Infof("authorize again on host:%v", c.host)
			c.cli, err = c.fn()
			if err != nil {
				return err
			}
		} else {
			break
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, node := range nodes.Items {
		tmpnode.Name = node.Name
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
		oldnode, ok := c.nodes[tmpnode.Name]
		if !ok || oldnode != tmpnode {
			c.nodes[tmpnode.Name] = tmpnode.DeepCopy()
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
