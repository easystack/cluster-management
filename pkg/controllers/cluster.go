package controllers

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cluster-management/pkg/license"
	"github.com/cluster-management/pkg/tag"
	"github.com/cluster-management/pkg/utils/maps"

	v1 "github.com/cluster-management/pkg/api/v1"
	"github.com/cluster-management/pkg/k8s"
	oppkg "github.com/cluster-management/pkg/openstack"
	"github.com/cluster-management/pkg/utils"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/blockstorage/v2/volumes"
	"github.com/gophercloud/gophercloud/openstack/containerinfra/v1/clusters"
	"github.com/gophercloud/gophercloud/pagination"
	"k8s.io/klog/v2"
)

type ReadyNum int

const (
	SomeReady ReadyNum = iota
	AllReady
	AllNotReady
)

const (
	AnnotationPvcGcLabelKey = "eks-resource-gc/pvc"
	pvcSuffix               = "-dynamic-pvc-"

	healthApiKey = "api"
	ApiOk        = "ok"
)

type health struct {
	num    ReadyNum
	nodes  int
	apiok  bool
	reason []string
}

type RemoveCrErr int

func NewRemoveCrErr() error {
	return RemoveCrErr(0)
}

func (r RemoveCrErr) Error() string {
	return "remove"
}

type removeCinder struct {
	status error
	sync   bool
}

type Operate struct {
	k8mg         *k8s.Manage
	opmg         *oppkg.OpenMgr
	tagmg        *tag.TagMgr
	enableLeader bool

	nsnameSpec map[string]*v1.ClusterSpec
	//key: clusterid
	mgmu    sync.RWMutex
	magnums map[string]*v1.EksSpec
	// crRemoveDelayInterval should be greater than timeout that Magnum writes to the database
	crRemoveDelayInterval time.Duration

	//key: clusterid
	cindermu sync.RWMutex
	cinders  map[string]*removeCinder
}

type sorts []*v1.Node

func (s sorts) Swap(i, j int) {
	tmpn := s[i].DeepCopy()
	s[j].DeepCopyInto(s[i])
	tmpn.DeepCopyInto(s[j])
}

func (s sorts) Len() int {
	return len(s)
}

func (s sorts) Less(i, j int) bool {
	return s[i].Name < s[j].Name
}

func (c *Operate) mgFilter(page pagination.Page) {
	var (
		wg sync.WaitGroup
	)
	if page == nil {
		return
	}
	infos, err := clusters.ExtractClusters(page)
	if err != nil {
		klog.Errorf("extract clusters failed:%v", err)
		return
	}
	c.mgmu.Lock()
	defer c.mgmu.Unlock()

	klog.Infof("fetch %d cluster info from magnum, %d record is found", len(infos), len(c.magnums))

	c.opmg.WrapClient(func(pv *gophercloud.ProviderClient) {
		cli, err := openstack.NewContainerInfraV1(pv, gophercloud.EndpointOpts{Region: "RegionOne"})
		if err != nil {
			klog.Errorf("create magnum client failed:%v", err)
			return
		}
		for cid := range c.magnums {
			id := cid

			klog.Infof("start cluster %s sync", id)
			wg.Add(1)
			err = utils.Submit(func() {
				defer wg.Done()
				neweks := &v1.EksSpec{
					EksHealthReasons: make(map[string]string),
				}
				info, err := clusters.Get(cli, id).Extract()
				if err != nil {
					if _, ok := err.(gophercloud.ErrDefault404); ok {
						oldStatus := c.magnums[id].EksStatus
						if oldStatus == "" {
							timeNow := time.Now()
							timestamp := c.magnums[id].ClusterNotFoundTimestamp
							if timestamp.IsZero() {
								timestamp, c.magnums[id].ClusterNotFoundTimestamp = timeNow, timeNow
							}
							klog.Errorf("%s has gone, %s in total, cluster %s sync failed", timeNow.Sub(timestamp), c.crRemoveDelayInterval, id)
							if timestamp.Add(c.crRemoveDelayInterval).After(timeNow) {
								return
							}
							// after crRemoveDelayInterval, it is also means Magnum writes
							// to the database failed. cr will be deleted.
						}
						klog.Infof("cluster %s is not found, cr will be deleted", id)
						// EksSpec.EksClusterID will be used to check exist or not!
						neweks.EksStatus = oldStatus
						neweks.Hadsync = true
						neweks.DeepCopyInto(c.magnums[id])
						return
					}
					klog.Errorf("show cluster %s failed: %v", id, err)
					return
				}
				klog.V(6).Infof("find match cluster: %v", info)
				neweks.EksStatus = info.Status
				neweks.EksReason = info.StatusReason
				neweks.EksFaults = info.Faults
				neweks.EksClusterID = info.UUID
				neweks.EksName = info.Name
				neweks.APIAddress = info.APIAddress
				neweks.EksStackID = info.StackID
				neweks.CVersion = info.COEVersion
				neweks.CreationTimestamp = info.CreatedAt.Unix()
				for k, v := range info.HealthStatusReason {
					if s, ok := v.(string); ok {
						neweks.EksHealthReasons[k] = s
					}
				}
				neweks.Hadsync = true
				neweks.DeepCopyInto(c.magnums[id])
				klog.Infof("update cluster info successfully: %v", neweks)
			})
			if err != nil {
				wg.Done()
				klog.Errorf("submit task cluster show failed:%v", err)
				c.magnums[id].EksHealthReasons[healthApiKey] = "submit task failed"
			}
		}
		wg.Wait()
	})
}

func (c *Operate) cinderDeleteFn(page pagination.Page) {
	if page == nil {
		return
	}
	infos, err := volumes.ExtractVolumes(page)
	if err != nil {
		klog.Errorf("extract Volumes failed:%v", err)
		return
	}
	c.cindermu.Lock()
	defer c.cindermu.Unlock()

	if len(c.cinders) == 0 {
		return
	}
	var (
		dels = make(map[string][]string)
		wg   sync.WaitGroup
	)
	buf := utils.GetBuf()
	defer utils.PutBuf(buf)
	for _, volume := range infos {
		volname := utils.Str2bytes(volume.Name)
		for id, rmv := range c.cinders {
			buf.Reset()
			buf.WriteString(id)
			buf.WriteString(pvcSuffix)
			if rmv.sync {
				continue
			}
			if !bytes.HasPrefix(volname, buf.Bytes()) {
				continue
			}
			klog.Infof("append delete volume %v on cluster %v", volume.Name, id)
			dels[id] = append(dels[id], volume.ID)
		}
	}
	for id, ids := range dels {
		klog.Infof("There are %d volumes associated with the PVC under cluster %s need to be deleted", len(dels[id]), id)
		var (
			errBuf    = utils.GetBuf()
			newcinder = &removeCinder{
				sync: true,
			}
			newids = make([]string, len(ids))
			newid  = id
		)
		wg.Add(1)
		copy(newids, ids)
		err = utils.Submit(func() {
			wg.Done()
			newcinder.sync = true
			c.opmg.WrapClient(func(pv *gophercloud.ProviderClient) {
				cli, err := openstack.NewBlockStorageV2(pv, gophercloud.EndpointOpts{})
				if err != nil {
					newcinder.status = err
					return
				} else {
					//will try delete all cinder
					for _, volumeid := range newids {
						err = volumes.Delete(cli, volumeid, volumes.DeleteOpts{}).ExtractErr()
						if err != nil {
							errBuf.WriteString(err.Error())
						}
					}
					if errBuf.Len() != 0 {
						newcinder.status = fmt.Errorf(errBuf.String())
					}
				}
			})
			c.cinders[newid] = newcinder
			utils.PutBuf(errBuf)
		})
		if err != nil {
			wg.Done()
			newcinder.status = fmt.Errorf("submit task failed")
			newcinder.sync = true
		}
	}
	wg.Wait()
	//not found means volume had deleted
	for _, rmv := range c.cinders {
		if !rmv.sync {
			rmv.status = nil
			rmv.sync = true
		}
	}

}

func NewCluster(k8mg *k8s.Manage, opmg *oppkg.OpenMgr, tagmg *tag.TagMgr, enableLeader bool, crRemoveDelay time.Duration) *Operate {
	op := &Operate{
		k8mg:                  k8mg,
		opmg:                  opmg,
		tagmg:                 tagmg,
		mgmu:                  sync.RWMutex{},
		magnums:               make(map[string]*v1.EksSpec),
		nsnameSpec:            make(map[string]*v1.ClusterSpec),
		enableLeader:          enableLeader,
		cinders:               make(map[string]*removeCinder),
		crRemoveDelayInterval: crRemoveDelay,
	}
	opmg.Regist(oppkg.Magnum, op.mgFilter)
	opmg.Regist(oppkg.Cinder, op.cinderDeleteFn)
	return op
}

func (s *Operate) Start(ctx context.Context) error {
	go s.k8mg.LoopRun(ctx)
	s.opmg.Run()
	s.tagmg.Run(ctx)
	return nil
}

func (s *Operate) NeedLeaderElection() bool {
	return s.enableLeader
}

//sync status node, ClusterStatus
func (c *Operate) k8status(clust *v1.Cluster) ([]*v1.Node, error) {
	var (
		host = clust.Spec.Host
		err  error
	)
	if host == "" {
		klog.Errorf("not found spec.host on %s", clust.GetName())
		return nil, fmt.Errorf("not found host")
	}
	cli, err := c.k8mg.Get(host)
	if err != nil {
		klog.Errorf("get k8s client failed:%v", err)
		return nil, err
	}
	if !cli.HadSyncd() {
		klog.Infof("k8s client not synced on %v", clust.GetName())
		return nil, nil
	}
	nodes, nerr := cli.Nodes()
	if nerr != nil {
		klog.Errorf("get nodes failed:%v", nerr)
		return nil, nerr
	}
	if len(nodes) == 0 {
		klog.Infof("get nodes length is zero, should not be here")
		return nil, nil
	}
	reOrderNodes(nodes)
	return nodes, nil
}

func (c *Operate) handler(clust *v1.Cluster) error {
	var (
		status = &clust.Status
		spec   = &clust.Spec
	)
	nodes, err := c.k8status(clust)
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		return nil
	}

	if host, err := parseHostname(spec.Host); err == nil {
		status.ClusterInfo.FloatingIP = host
	} else {
		klog.Errorf("parse floating ip failed: %v", err)
	}

	if len(status.Nodes) != len(nodes) {
		status.Nodes = nodes
	} else {
		for i, v := range status.Nodes {
			nodes[i].DeepCopyInto(v)
		}
	}
	for _, no := range nodes {
		spec.Architecture = no.Arch
		spec.Version = no.Version
		break
	}

	return nil
}

func (c *Operate) pvcReclaim(clust *v1.Cluster) error {
	var (
		spec = &clust.Spec
	)
	c.cindermu.Lock()
	defer c.cindermu.Unlock()
	v, ok := c.cinders[spec.ClusterID]
	if !ok {
		c.cinders[spec.ClusterID] = &removeCinder{}
		return fmt.Errorf("wait delete volume under cluster %s", spec.ClusterID)
	}
	if v.sync {
		//try delete again, util success
		v.sync = false
		return v.status
	}
	return fmt.Errorf("wait delete volume under cluster %s", spec.ClusterID)
}

func (c *Operate) ekshandler(clust *v1.Cluster, lic *license.License) (rerr error) {
	var (
		spec     = &clust.Spec
		status   = &clust.Status
		err      error
		removecr bool
	)
	// if eks cluster has been deleted, next delete the releated cr resource
	// (1): first check cr annotation key, if cr annotation has "eksPvcGCKey=true"
	//      cluster controller will clean up resources those eks left
	// (2): if clean up all resources successed, next remove eksPvcGCKey in cr annotation(
	//      if cleaning up pvc successed, remove eksPvcGCKey) and then delete cr
	// (3): if clean up resources failed, in next loop controller will continue delete those resources
	//      Tips: controller will retry 3 times in cleaning up resources
	if spec.ClusterID == "" {
		klog.Errorf("not found cluster id on %s", clust.GetName())
		return fmt.Errorf("not found cluster_id")
	}
	defer func() {
		if err != nil {
			return
		}
		if removecr {
			if _, ok := clust.Annotations[AnnotationPvcGcLabelKey]; !ok {
				rerr = NewRemoveCrErr()
			}
		}
	}()

	if _, ok := clust.Annotations[AnnotationPvcGcLabelKey]; ok {
		// if annotaions gc key exist, means resource should delete by myself
		//(TODO) whether it's or not correct design, but should forward compatible
		klog.Infof("find annotaions label %v, start pvc reclaim", AnnotationPvcGcLabelKey)
		err = c.pvcReclaim(clust)
		if err != nil {
			klog.Errorf("delete pvc failed:%v", err)
		} else {
			klog.Infof("delete pvc in cluster %v success", clust.Name)
			delete(clust.Annotations, AnnotationPvcGcLabelKey)
		}
	}
	c.mgmu.Lock()
	_, ok := c.magnums[spec.ClusterID]
	if !ok {
		c.magnums[spec.ClusterID] = spec.Eks.DeepCopy()
	}
	neweks := c.magnums[spec.ClusterID]
	if !neweks.Hadsync {
		klog.Infof("%v not synced, skip", clust.Name)
		c.mgmu.Unlock()
		return nil
	} else if neweks.EksClusterID == "" {
		// only if eks cluster info has synced and cluster id
		// has been cleaned, cr could be deleted.
		removecr = true
		c.mgmu.Unlock()
		return nil
	}
	neweks.Hadsync = false
	if !reflect.DeepEqual(neweks, &spec.Eks) {
		klog.Infof("copy eks info from magnum: %v", neweks)
		neweks.DeepCopyInto(&spec.Eks)
	}
	c.mgmu.Unlock()
	// clear the reasons
	status.ClusterStatusReason = v1.ClusterStatusReason{}
	if strings.HasSuffix(neweks.EksStatus, "COMPLETE") {
		// magnum info is not correct
		health := parseMagnumHealths(neweks.EksHealthReasons)
		if health != nil {
			spec.Nodes = health.nodes
			switch health.num {
			case AllNotReady:
				status.ClusterStatus = v1.ClusterDisConnected
			case AllReady:
				status.ClusterStatus = v1.ClusterHealthy
			case SomeReady:
				status.ClusterStatus = v1.ClusterWarning
			}
			if !health.apiok {
				status.ClusterStatus = v1.ClusterDisConnected
			}
			status.ClusterStatusReason.StatusReason = neweks.EksReason
			status.ClusterStatusReason.Faults = append(status.ClusterStatusReason.Faults, health.reason...)
			//(TODO) the magnum bug, when cluster delete failed.
			// the number is also ok and ready
		}
	} else {
		//(TODO) have to set clusterstatus, when connect refused
		// handler do not update status, so update now
		status.ClusterStatus = v1.ClusterStat(neweks.EksStatus)
		status.ClusterStatusReason.StatusReason = neweks.EksReason
		status.ClusterStatusReason.Faults = append(status.ClusterStatusReason.Faults, maps.ToSlice(neweks.EksFaults)...)
	}
	// won't check license when cluster is in DELETE_XXX status
	if lic != nil && !strings.HasPrefix(neweks.EksStatus, "DELETE") {
		isIllegal, faultReason := lic.CheckNodes(status.Nodes)
		if isIllegal {
			status.ClusterStatus = v1.ClusterUnauthorized
			status.ClusterStatusReason.StatusReason = "License Prohibited"
			status.ClusterStatusReason.Faults = faultReason
		}
	}

	spec.Host = spec.Eks.APIAddress

	if spec.Host == "" {
		klog.Infof("%v not found apiaddress, skip", clust.Name)
		return nil
	}
	err = c.handler(clust)
	if err != nil {
		klog.Errorf("%v handler k8s failed: %v", clust.Name, err)
	}
	// 取当前平台; magnum 侧的coe_version
	spec.Version = neweks.CVersion
	return nil
}

func (c *Operate) ehoshandler(clust *v1.Cluster) error {
	return c.handler(clust)
}

func (c *Operate) eoshandler(clust *v1.Cluster) error {
	return c.handler(clust)
}

func (c *Operate) Process(clust *v1.Cluster, nsname string, lic *license.License) error {
	var (
		spec   = &clust.Spec
		status = &clust.Status
		err    error
	)
	klog.Infof("START %v, type:%v", clust.GetName(), spec.Type)
	switch spec.Type {
	case v1.ClusterEHOS:
		err = c.ehoshandler(clust)
	case v1.ClusterEKS:
		err = c.ekshandler(clust, lic)
		if spec.ClusterID != "" {
			if _, ok := c.nsnameSpec[nsname]; !ok {
				newspec := spec.DeepCopy()
				newspec.Eks = v1.EksSpec{}
				c.nsnameSpec[nsname] = newspec
			}
		}
	case v1.ClusterEOS:
		err = c.eoshandler(clust)
	default:
		err = fmt.Errorf("spec.type %s not support", spec.Type)
	}

	updateCondition(status, err)
	return err
}

func (c *Operate) Delete(nsname string) {
	v, ok := c.nsnameSpec[nsname]
	if !ok {
		return
	}

	c.k8mg.Del(v.Host)
	c.mgmu.Lock()
	delete(c.magnums, v.ClusterID)
	c.mgmu.Unlock()
	c.cindermu.Lock()
	delete(c.cinders, v.ClusterID)
	c.cindermu.Unlock()
}

func reOrderNodes(nodes []*v1.Node) {
	if len(nodes) == 0 {
		return
	}
	nos := sorts(nodes)
	sort.Sort(nos)
}

func updateCondition(stat *v1.ClusterStatus, err error) {
	if err == nil {
		return
	}
	for _, cond := range stat.Conditions {
		if cond.Reason == err.Error() {
			return
		}
	}
	stat.Conditions = append(stat.Conditions, &v1.Condition{
		LastUpdateTime: time.Now().Format(time.RFC3339),
		Reason:         err.Error(),
	})
}

func parseMagnumHealths(mm map[string]string) *health {
	var (
		nodecount    int
		ready        int
		notready     int
		healthReason []string
		num          ReadyNum = SomeReady
	)
	for k, v := range mm {
		if k == healthApiKey {
			if v != ApiOk {
				return &health{
					apiok:  false,
					num:    AllNotReady,
					reason: append(healthReason, v),
				}
			}
			continue
		}
		nodecount++
		if v == "True" {
			ready++
		} else {
			notready++
			healthReason = append(healthReason, fmt.Sprintf("%s is %s", k, v))
		}
	}
	if nodecount == 0 {
		// If nodecount is 0, it means mm from Magnum health_status_reason
		// is empty. It shows that cluster just created may not start ready.
		// health only show the status of a started cluster.
		klog.Errorf("0 node info was found in magnum")
		return nil
	}
	if ready == 0 && notready == nodecount {
		num = AllNotReady
	}
	if notready == 0 && ready == nodecount {
		num = AllReady
	}

	return &health{
		num:    num,
		apiok:  true,
		nodes:  nodecount,
		reason: healthReason,
	}
}

func parseHostname(rawurl string) (string, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return "", err
	}
	return u.Hostname(), nil
}
