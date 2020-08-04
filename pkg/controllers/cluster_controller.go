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
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"reflect"
	"strings"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	cli "sigs.k8s.io/controller-runtime/pkg/client"

	ecnsv1 "github.com/cluster-management/pkg/api/v1"
	"github.com/cluster-management/pkg/k8s"
	"github.com/cluster-management/pkg/utils"
)

const (
	loggerCtxKey = "logger"
)

const (
	clusterTypeEks          = "EKS"
	errorEksClusterNotFound = "Resource not found"
)

const (
	//eksCreateInProgress    = "CREATE_IN_PROGRESS"
	//eksCreateFailed        = "CREATE_FAILED"
	//eksCreateComplete      = "CREATE_COMPLETE"
	//eksUpdateInProgress    = "UPDATE_IN_PROGRESS"
	//eksUpdateFailed        = "UPDATE_FAILED"
	//eksUpdateComplete      = "UPDATE_COMPLETE"
	//eksDeleteInProgress    = "DELETE_IN_PROGRESS"
	//eksDeleteFailed        = "DELETE_FAILED"
	eksDeleteComplete = "DELETE_COMPLETE"
	//eksRollbackInProgress  = "ROLLBACK_IN_PROGRESS"
	//eksRollbackFailed      = "ROLLBACK_FAILED"
	//eksRollbackComplete    = "ROLLBACK_COMPLETE"
)

type clusterCache struct {
	mu         sync.Mutex
	clusterMap map[string]ecnsv1.Cluster
}

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	client        cli.Client
	log           logr.Logger
	cache         *clusterCache
	k8sService    *k8s.KService
	pollingPeriod int
}

func NewClusterReconciler(c cli.Client, logger logr.Logger, k8s *k8s.KService, period int) *ClusterReconciler {
	return &ClusterReconciler{
		client:        c,
		log:           logger,
		cache:         &clusterCache{clusterMap: make(map[string]ecnsv1.Cluster)},
		k8sService:    k8s,
		pollingPeriod: period,
	}
}

// +kubebuilder:rbac:groups=ecns.easystack.com,resources=Clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ecns.easystack.com,resources=Clusters/status,verbs=get;update;patch

func (r *ClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	rootCtx := context.Background()
	logger := r.log.WithValues("Reconcile", req.NamespacedName)
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)
	key := req.Namespace + req.Name

	// main logic here
	var cluster ecnsv1.Cluster
	if err := r.client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		logger.Error(err, "unable to fetch Cluster", "The Cluster Key is", key)
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, cli.IgnoreNotFound(err)
	}
	cached, ok := r.cache.get(key)

	// name of our custom finalizer
	myFinalizerName := "cluster-management.namespaceresources.finalizers"

	// examine DeletionTimestamp to determine if object is under deletion
	if cluster.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !containsString(cluster.ObjectMeta.Finalizers, myFinalizerName) && cluster.Spec.Type != clusterTypeEks {
			cluster.ObjectMeta.Finalizers = append(cluster.ObjectMeta.Finalizers, myFinalizerName)
			if err := r.client.Update(ctx, &cluster); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		// do not clean up resources in eks before delete cr, because eks cluster has been deleted
		if containsString(cluster.ObjectMeta.Finalizers, myFinalizerName) && cluster.Spec.Type != clusterTypeEks {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(&cluster, ctx); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			cluster.ObjectMeta.Finalizers = removeString(cluster.ObjectMeta.Finalizers, myFinalizerName)
			if err := r.client.Update(ctx, &cluster); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	if !ok {
		// Add event
		// Reconcile filling information can be done in polling action, so we will not do
		// filling in add event, this can avoid race condition in cr resource
		logger.Info("Add Event", "CRD Spec", cluster)
		if cluster.Spec.Type == clusterTypeEks {
			if !strings.HasSuffix(cluster.Spec.Eks.EksStatus, "COMPLETE") || cluster.Spec.Host == "" {
				logger.Info("Check Eks Cluster Status", "ClusterID", cluster.Spec.ClusterID, "Status", cluster.Status.ClusterStatus)
				return ctrl.Result{Requeue: true}, nil
			}
		}

		r.k8sService.Host = cluster.Spec.Host

		logger.Info("Add Event", "Assign Namespace Resources To Cluster", key)
		if err := r.k8sService.AssignClusterToProjects(ctx, &cluster, cluster.Spec.Projects); err != nil {
			return ctrl.Result{}, err
		}

		// if all action succeed with cluster, we set .status.needreconcile to true
		cluster.Status.HasReconciledOnce = true
		if err := r.client.Update(ctx, &cluster); err != nil {
			logger.Error(err, "Failed to update Cluster cr status.needreconcile")
			return ctrl.Result{}, err
		}
		// Last we set this to cache when all action succeed
		r.cache.set(key, cluster)

	} else {
		// Update event
		logger.Info("Update Event", "Cached", cached)
		logger.Info("Update Event", "Desired", cluster)

		if reflect.DeepEqual(cached.Spec.Projects, cluster.Spec.Projects) {
			logger.Info("Do nothing if projects not change")
		} else {
			logger.Info("Update Cluster projects info")

			if err := r.updateClusterProjects(ctx, &cluster, cached.Spec.Projects, cluster.Spec.Projects); err != nil {
				return ctrl.Result{}, err
			}
			// Last we set this to cache when all action succeed
			r.cache.set(key, cluster)
		}
	}

	return ctrl.Result{}, nil
}

// before controller running, we should cache all crs which has already exist and which Status.HasReconcileOnce
// is true, that means those crs which already reconciled once do not need to added again when controller start/restart
func (r *ClusterReconciler) MakeSourceReadyBeforeReconcile() {
	rootCtx := context.Background()
	logger := r.log.WithName("BeforeReconcile")
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)
	var clusterList ecnsv1.ClusterList
	// Get all Cluster CRD
	err := r.client.List(ctx, &clusterList)
	if err != nil {
		logger.Error(err, "Failed to list Clusters")
		return
	}

	for _, c := range clusterList.Items {
		if hasReconciled := c.Status.HasReconciledOnce; hasReconciled {
			clusterName := c.ObjectMeta.Name
			clusterNamespace := c.ObjectMeta.Namespace
			key := clusterName + clusterNamespace
			logger.Info("Cache clusters before reconcile", "Cluster", clusterName)
			r.cache.set(key, c)
		}
	}
	return
}

func (r *ClusterReconciler) PollingClusterInfo() {
	stopCh := make(chan struct{})
	wait.Until(r.pollingClusterLoop, time.Duration(r.pollingPeriod)*time.Second, stopCh)
}

func (r *ClusterReconciler) pollingClusterLoop() {
	rootCtx := context.Background()
	logger := r.log.WithName("Polling")
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)

	var clusterList ecnsv1.ClusterList
	// Get all Cluster CRD
	err := r.client.List(ctx, &clusterList)
	if err != nil {
		logger.Error(err, "Failed to list Clusters")
		return
	}

	for i := range clusterList.Items {
		//logger.Info("[Polling]:", "Cluster", &clusterList.Items[i].Name)
		if err := r.pollingAndUpdate(ctx, &clusterList.Items[i], r.k8sService); err != nil {
			logger.Error(err, "Failed to pollingAndUpdate cluster", "The Cluster is", &clusterList.Items[i])
		}
	}
}

func (r *ClusterReconciler) pollingAndUpdate(ctx context.Context, cluster *ecnsv1.Cluster, k8sService *k8s.KService) error {
	logger := utils.GetLoggerOrDie(ctx)

	if cluster.Spec.Type == clusterTypeEks {
		ok, _ := r.checkAndUpdateEksCluster(ctx, cluster, k8sService)
		if !ok {
			logger.Info("Check Eks Until In Complete Status", "ClusterID", cluster.Spec.ClusterID, "Status", cluster.Spec.Eks.EksStatus)
			return nil
		}
	}
	k8sService.Host = cluster.Spec.Host

	whetherUpdate, err := k8sService.CheckClusterInfo(ctx, cluster)
	if err != nil {
		logger.Error(err, "Failed to check cluster latest info")
		return err
	}

	if whetherUpdate {
		logger.Info("After Polling", "After", cluster)
		err = r.updateClusterCRD(ctx, cluster)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ClusterReconciler) checkAndUpdateEksCluster(ctx context.Context, cluster *ecnsv1.Cluster, k8sService *k8s.KService) (bool, error) {
	logger := utils.GetLoggerOrDie(ctx)
	osClient := k8sService.OSClient
	check := false
	whetherToUpdate := false
	eksSpec, err := osClient.GetMagnumClusterStatus(ctx, cluster.Spec.ClusterID)
	if err != nil {
		if err.Error() != errorEksClusterNotFound {
			return false, err
		} else {
			// if eks cluster has been deleted, then delete the releated cr resource
			logger.Info("Try to delete eks cr resource", "ClusterName", cluster.Name, "EKSClusterId", cluster.Spec.ClusterID)
			err := r.client.Delete(ctx, cluster)
			if err != nil {
				logger.Error(err, "Failed to delete eks cr resource", "ClusterName", cluster.Name, "EKSClusterId", cluster.Spec.ClusterID)
				return false, err
			}
			return false, nil
		}
	}

	// when only eks in complete status, can polling eks node status
	if strings.HasSuffix(eksSpec.EksStatus, "COMPLETE") {
		if eksSpec.EksStatus != eksDeleteComplete {
			check = true

			if eksSpec.APIAddress != "" && cluster.Spec.Host != eksSpec.APIAddress {
				cluster.Spec.Host = eksSpec.APIAddress
				whetherToUpdate = true
			}
		}
	}

	if !reflect.DeepEqual(cluster.Spec.Eks, eksSpec) {
		whetherToUpdate = true
		cluster.Spec.Eks = eksSpec
		// if eks not in COMPLETE status, we should also to add eks status to cluster cr.Status.ClusterStatus
		if cluster.Status.ClusterStatus != eksSpec.EksStatus && !strings.HasSuffix(eksSpec.EksStatus, "COMPLETE") {
			cluster.Status.ClusterStatus = eksSpec.EksStatus
			message := fmt.Sprintf("[%s] Cluster is in %s Status, when cluster in COMPLETE status, it will be reconciled in next loop", "EKS", eksSpec.EksStatus)
			cluster.Status.ClusterStatusReason = append(cluster.Status.ClusterStatusReason, message)
		}
	}

	if whetherToUpdate {
		logger.Info("Update EKS Cr Resource In Polling Step", "The Cluster Detail is", cluster)
		err := r.updateClusterCRD(ctx, cluster)
		if err != nil {
			logger.Error(err, "Failed to update cluster CRD")
		}
	}

	return check, nil
}

func (r *ClusterReconciler) updateClusterCRD(ctx context.Context, cluster *ecnsv1.Cluster) error {
	logger := utils.GetLoggerOrDie(ctx)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.client.Update(ctx, cluster); err != nil {
			logger.Error(err, "Failed to update Cluster CRD")
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update Cluster %s: %v", cluster.Name, err)
	}
	return nil
}

func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ecnsv1.Cluster{}).
		Complete(r)
}

func (r *ClusterReconciler) updateClusterProjects(ctx context.Context, cluster *ecnsv1.Cluster, cached []string, desired []string) error {

	added := make([]string, 0)
	removed := make([]string, 0)

	for _, p := range desired {
		if !utils.StringInSlice(p, cached) {
			added = append(added, p)
		}
	}
	if err := r.k8sService.AssignClusterToProjects(ctx, cluster, added); err != nil {
		return err
	}

	for _, p := range cached {
		if !utils.StringInSlice(p, desired) {
			removed = append(removed, p)
		}
	}
	if err := r.k8sService.UnAssignClusterToProjects(ctx, cluster, removed); err != nil {
		return err
	}

	return nil
}

// get a cached cluster
func (s *clusterCache) get(key string) (ecnsv1.Cluster, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cluster, ok := s.clusterMap[key]
	return cluster, ok
}

// set a cluster cache
func (s *clusterCache) set(key string, cluster ecnsv1.Cluster) {
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

func (r *ClusterReconciler) deleteExternalResources(cluster *ecnsv1.Cluster, ctx context.Context) error {
	//
	// delete any external resources associated with the cronJob
	//
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple types for same object
	// Delete event

	// if eks cr deleted, no need to unassign because eks cluster has been deleted
	if cluster.Spec.Type != clusterTypeEks {
		logger := r.log.WithValues("DeleteExternalResources", "createdNamespaceResources")
		logger.Info("Begin deleteExternalResources", "The Cluster namespace is", cluster.Spec.Projects)
		if err := r.k8sService.UnAssignClusterToProjects(ctx, cluster, cluster.Spec.Projects); err != nil {
			return err
		}
	}
	return nil
}

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}
