/*
Copyright 2020 EasyStack Container Team.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"github.com/cluster-management/utils"
	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	cli "sigs.k8s.io/controller-runtime/pkg/client"
	"sync"
	"time"

	eosv1 "github.com/cluster-management/api/v1"
	"github.com/cluster-management/k8s"
	"github.com/cluster-management/openstack"
)

const (
	loggerCtxKey = "logger"
)

type clusterCache struct {
	mu         sync.Mutex
	clusterMap map[string]eosv1.EosCluster
}

// EosClusterReconciler reconciles a EosCluster object
type EosClusterReconciler struct {
	client     cli.Client
	log        logr.Logger
	authOpts   *gophercloud.AuthOptions
	cache      *clusterCache
	osService  *openstack.OSService
	k8sService *k8s.KService
}

func NewEosClusterReconciler(c cli.Client, logger logr.Logger, opts *gophercloud.AuthOptions, os *openstack.OSService, k8s *k8s.KService) *EosClusterReconciler {
	return &EosClusterReconciler{
		client:     c,
		log:        logger,
		authOpts:   opts,
		cache:      &clusterCache{clusterMap: make(map[string]eosv1.EosCluster)},
		osService:  os,
		k8sService: k8s,
	}
}

// +kubebuilder:rbac:groups=eos.exampel.org,resources=eosclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=eos.exampel.org,resources=eosclusters/status,verbs=get;update;patch

func (r *EosClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	rootCtx := context.Background()
	logger := r.log.WithValues("Reconcile", req.NamespacedName)
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)
	key := req.Namespace + req.Name

	// main logic here
	var cluster eosv1.EosCluster
	err := r.client.Get(ctx, req.NamespacedName, &cluster)
	cached, ok := r.cache.get(key)

	// Delete event
	if err != nil && apierrs.IsNotFound(err) {
		logger.Info("Delete Event", "Cluster has been deleted", req.NamespacedName)
		r.k8sService.UnAssignClusterToProjects(ctx, &cached, cached.Spec.Projects)
		r.cache.delete(key)
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if !ok {
		// Add event
		logger.Info("Add Event", "CRD Spec", cluster)
		r.k8sService.Host = cluster.Spec.Host
		err = r.k8sService.GetClusterInfo(ctx, &cluster, r.osService)
		if err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("Add Event", "Latest Info", cluster)

		r.cache.set(key, cluster)

		r.k8sService.AssignClusterToProjects(ctx, &cluster, cluster.Spec.Projects)

		err = r.updateClusterCRD(ctx, &cluster)
		if err != nil {
			return ctrl.Result{}, err
		}

	} else {
		// Update event
		logger.Info("Update Event", "Cached", cached)
		logger.Info("Update Event", "Desired", cluster)

		if reflect.DeepEqual(cached.Spec.Projects, cluster.Spec.Projects) {
			logger.Info("Do nothing if projects not change")
		} else {
			logger.Info("Update Cluster projects info")
			r.cache.set(key, cluster)
			r.updateClusterProjects(ctx, &cluster, cached.Spec.Projects, cluster.Spec.Projects)
		}
	}

	return ctrl.Result{}, nil
}

func (r *EosClusterReconciler) PollingClusterInfo() error {
	rootCtx := context.Background()
	logger := r.log.WithName("Polling")
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)

	var clusterList eosv1.EosClusterList

	for {
		time.Sleep(3 * time.Second)
		fmt.Println("polling clusters latest info")

		// Get all EOSCluster CRD
		err := r.client.List(ctx, &clusterList)
		if err != nil {
			logger.Error(err, "Failed to list EOSClusters")
		}

		for i := range clusterList.Items {
			r.pollingAndUpdate(ctx, &clusterList.Items[i], r.k8sService, r.osService)
		}
	}
}

func (r *EosClusterReconciler) pollingAndUpdate(ctx context.Context, cluster *eosv1.EosCluster, k8sService *k8s.KService, osService *openstack.OSService) error {
	logger := utils.GetLoggerOrDie(ctx)

	logger.Info("Before Polling", "Before", cluster)
	k8sService.Host = cluster.Spec.Host
	err := k8sService.GetClusterInfo(ctx, cluster, osService)
	if err != nil {
		logger.Error(err, "Failed to get cluster latest info")
	}

	logger.Info("After Polling", "After", cluster)
	err = r.updateClusterCRD(ctx, cluster)
	if err != nil {
		logger.Error(err, "Failed to update cluster CRD")
	}

	return nil
}

func (r *EosClusterReconciler) updateClusterCRD(ctx context.Context, cluster *eosv1.EosCluster) error {
	logger := utils.GetLoggerOrDie(ctx)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.client.Update(ctx, cluster); err != nil {
			logger.Error(err, "Failed to update EOSCluster CRD")
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update EOSCluster %s: %v", cluster.Name, err)
	}
	return nil
}

func (r *EosClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&eosv1.EosCluster{}).
		Complete(r)
}

func (r *EosClusterReconciler) updateClusterProjects(ctx context.Context, cluster *eosv1.EosCluster, cached []string, desired []string) error {

	added := make([]string, 0)
	removed := make([]string, 0)

	for _, p := range desired {
		if !utils.StringInSlice(p, cached) {
			added = append(added, p)
		}
	}
	r.k8sService.AssignClusterToProjects(ctx, cluster, added)

	for _, p := range cached {
		if !utils.StringInSlice(p, desired) {
			removed = append(removed, p)
		}
	}
	r.k8sService.UnAssignClusterToProjects(ctx, cluster, removed)

	return nil
}

// get a cached cluster
func (s *clusterCache) get(key string) (eosv1.EosCluster, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cluster, ok := s.clusterMap[key]
	return cluster, ok
}

// set a cluster cache
func (s *clusterCache) set(key string, cluster eosv1.EosCluster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clusterMap[key] = cluster
}

// delete a cached cluster
func (s *clusterCache) delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clusterMap, key)
}
