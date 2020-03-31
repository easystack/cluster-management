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
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"


	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	eosv1 "github.com/cluster-management/api/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	loggerCtxKey = "logger"
)

// EosClusterReconciler reconciles a EosCluster object
type EosClusterReconciler struct {
	client.Client
	Log logr.Logger
	AuthOpts *gophercloud.AuthOptions
}

// +kubebuilder:rbac:groups=eos.exampel.org,resources=eosclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=eos.exampel.org,resources=eosclusters/status,verbs=get;update;patch

func (r *EosClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	rootCtx := context.Background()
	logger := r.Log.WithValues("eoscluster", req.NamespacedName)
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

	// Get token
	token, err := r.getKeystoneToken(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}


	// Get cluster info
	r.getClusterInfo(ctx, &cluster, token)

	// Update cluster CRD


	return ctrl.Result{}, nil
}



func (r *EosClusterReconciler) getKeystoneToken(ctx context.Context) (*tokens.Token, error) {
	logger := getLoggerOrDie(ctx)
	provider, err := openstack.AuthenticatedClient(*r.AuthOpts)
	if err != nil {
		logger.Error(err, "Failed to Authenticate to OpenStack")
		return nil, err
	}
	client, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{Region: "RegionOne"})
	if err != nil {
		logger.Error(err, "Failed to Initialize Keystone client")
		return nil, err
	}
	token, err := tokens.Create(client, r.AuthOpts).ExtractToken()
	if err != nil {
		logger.Error(err, "Failed to Get token")
		return nil, err
	}

	return token, nil
}

func (r *EosClusterReconciler) getClusterInfo(ctx context.Context, cluster *eosv1.EosCluster, token *tokens.Token) {
	cs, _ := r.getK8sClient(ctx, cluster, token)
	nodes, _ := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})

	fmt.Printf("%+v\n",nodes)

}

func (r *EosClusterReconciler) getK8sClient(ctx context.Context, cluster *eosv1.EosCluster, token *tokens.Token) (*kubernetes.Clientset, error){
	logger := getLoggerOrDie(ctx)

	// creates the clientset
	config := rest.Config{
		Host: cluster.Spec.Host,
		BearerToken: token.ID,
	}
	cs, err := kubernetes.NewForConfig(&config)
	if err != nil {
		logger.Error(err, "Failed to create k8s client")
		return nil, err
	}
	return cs, nil
}

func (r *EosClusterReconciler) updateClusters(ctx context.Context, cluster *eosv1.EosCluster) {

}

func getLoggerOrDie(ctx context.Context) logr.Logger {
	logger, ok := ctx.Value(loggerCtxKey).(logr.Logger)
	if !ok {
		panic("context didn't contain logger")
	}
	return logger
}

func (r *EosClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&eosv1.EosCluster{}).
		Complete(r)
}