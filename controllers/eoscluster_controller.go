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
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"

	eosv1 "github.com/cluster-management/api/v1"
	k8sservice "github.com/cluster-management/k8s"
	osservice "github.com/cluster-management/openstack"
)

const (
	loggerCtxKey = "logger"
)

// EosClusterReconciler reconciles a EosCluster object
type EosClusterReconciler struct {
	client.Client
	Log      logr.Logger
	AuthOpts *gophercloud.AuthOptions
}

// +kubebuilder:rbac:groups=eos.exampel.org,resources=eosclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=eos.exampel.org,resources=eosclusters/status,verbs=get;update;patch

func (r *EosClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	rootCtx := context.Background()
	logger := r.Log.WithValues("Reconcile", req.NamespacedName)
	ctx := context.WithValue(rootCtx, loggerCtxKey, logger)

	// your logic start here
	var cluster eosv1.EosCluster
	err := r.Get(ctx, req.NamespacedName, &cluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Cluster is in the process of being deleted, so no need to do anything.
	if cluster.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	// Get keystone token to access kubernetes API
	var osClient = osservice.OSService{Opts: r.AuthOpts}
	token, err := osClient.GetKeystoneToken(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Get cluster latest info
	var k8sClient = k8sservice.KService{
		Host: cluster.Spec.Host,
		Token: token,
	}
	err = k8sClient.GetClusterInfo(ctx, &cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Update EOSCluster CRD
	err = r.updateClusterCRD(ctx, &cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *EosClusterReconciler) PollingClusterInfo() error {
	for {
		time.Sleep(2 * time.Second)
		fmt.Println("polling clusters latest info")

		rootCtx := context.Background()
		logger := r.Log.WithName("Polling")
	    ctx := context.WithValue(rootCtx, loggerCtxKey, logger)

	    // Get all EOSCluster CRD
	    var clusterList eosv1.EosClusterList
	    err := r.List(ctx, &clusterList)
		if err != nil {
			logger.Error(err, "Failed to list EOSClusters")
		}

	    for i := range clusterList.Items{
	    	fmt.Printf("%+v\n", clusterList.Items[i])
		}
    }
}

func (r *EosClusterReconciler) updateClusterCRD(ctx context.Context, cluster *eosv1.EosCluster) error {
	logger := utils.GetLoggerOrDie(ctx)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Client.Update(ctx, cluster); err != nil {
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