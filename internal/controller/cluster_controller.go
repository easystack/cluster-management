/*
Copyright 2026.

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

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	batonv1 "github.com/cluster-management/api/v1"
	"github.com/cluster-management/internal/openstack"
)

const (
	clusterKind = "Cluster"

	// usageSyncPeriod is the periodic resync of the node usage.
	usageSyncPeriod = 5 * time.Minute

	openStackMachineKind = "OpenStackMachine"
)

// ClusterReconciler maintains the Cluster status of clusters created through
// Cluster API: it ensures a Cluster object exists for every CAPI cluster and
// records the resource usage of all its nodes (machines).
type ClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// FlavorLister lists the flavor catalog of the OpenStack cloud.
	FlavorLister openstack.FlavorLister
}

// +kubebuilder:rbac:groups=baton.ecns.easystack.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=baton.ecns.easystack.io,resources=clusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=baton.ecns.easystack.io,resources=clusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=openstackmachines,verbs=get;list;watch

// Reconcile ensures a Cluster object exists for the Cluster API cluster of the
// same name and namespace and keeps its node usage status up to date.
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cluster := &batonv1.Cluster{}
	err := r.Get(ctx, req.NamespacedName, cluster)
	if apierrors.IsNotFound(err) {
		return r.createFromCAPICluster(ctx, req.NamespacedName)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get cluster %s: %w", req.NamespacedName, err)
	}
	if !cluster.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if !ownedByCAPICluster(cluster) {
		logf.FromContext(ctx).Info("Cluster is not owned by a Cluster API cluster, skipping usage sync",
			"cluster", req.NamespacedName)
		return ctrl.Result{}, nil
	}
	return r.syncUsage(ctx, cluster)
}

// createFromCAPICluster creates the Cluster object for a CAPI cluster of the
// same name and namespace. Deletion is handled by the owner reference: when
// the CAPI cluster is deleted, the Cluster object is garbage collected.
func (r *ClusterReconciler) createFromCAPICluster(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	capiCluster := &clusterv1.Cluster{}
	if err := r.Get(ctx, key, capiCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get cluster-api cluster %s: %w", key, err)
	}
	if !capiCluster.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	cluster := &batonv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(),
				Kind:       clusterKind,
				Name:       capiCluster.Name,
				UID:        capiCluster.UID,
				Controller: new(true),
			}},
		},
	}
	if err := r.Create(ctx, cluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("create cluster %s: %w", key, err)
	}
	// The create event triggers a new reconcile that syncs the usage.
	logf.FromContext(ctx).Info("Created Cluster for cluster-api cluster", "cluster", key)
	return ctrl.Result{}, nil
}

// syncUsage aggregates the flavor resources of all machines of the cluster
// into the Cluster status.
func (r *ClusterReconciler) syncUsage(ctx context.Context, cluster *batonv1.Cluster) (ctrl.Result, error) {
	machineList := &clusterv1.MachineList{}
	if err := r.List(ctx, machineList,
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{clusterv1.ClusterNameLabel: cluster.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("list machines of cluster %s/%s: %w", cluster.Namespace, cluster.Name, err)
	}

	// Resolve each distinct flavor once per sync; the flavor catalog is fetched
	// lazily, at most once per sync.
	flavors := make(map[string]batonv1.ResourceUsage)
	var catalog map[string]openstack.Flavor
	nodes := make([]batonv1.NodeInfo, 0, len(machineList.Items))
	total := batonv1.ClusterInfo{Nodes: int64(len(machineList.Items))}
	for _, machine := range machineList.Items {
		flavorName, err := r.machineFlavorName(ctx, &machine)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("resolve flavor of machine %s/%s: %w", cluster.Namespace, machine.Name, err)
		}
		usage, ok := flavors[flavorName]
		if !ok {
			if catalog == nil {
				if catalog, err = r.FlavorLister.ListFlavors(ctx); err != nil {
					return ctrl.Result{}, fmt.Errorf("list flavors: %w", err)
				}
			}
			flavor, ok := catalog[flavorName]
			if !ok {
				return ctrl.Result{}, fmt.Errorf("flavor %q not found in the openstack cloud", flavorName)
			}
			usage = batonv1.ResourceUsage{CPU: flavor.CPU, Memory: flavor.MemoryMiB}
			flavors[flavorName] = usage
		}
		nodes = append(nodes, batonv1.NodeInfo{
			Name:   machine.Name,
			Flavor: flavorName,
			ResourceUsage: batonv1.ResourceUsage{
				CPU:    usage.CPU,
				Memory: usage.Memory,
			},
		})
		total.ResourceUsage.CPU += usage.CPU
		total.ResourceUsage.Memory += usage.Memory
	}
	slices.SortFunc(nodes, func(a, b batonv1.NodeInfo) int { return strings.Compare(a.Name, b.Name) })

	base := cluster.DeepCopy()
	cluster.Status.NodeInfo = nodes
	cluster.Status.ClusterInfo = total
	if equality.Semantic.DeepEqual(base.Status, cluster.Status) {
		return ctrl.Result{RequeueAfter: usageSyncPeriod}, nil
	}
	if err := r.Status().Update(ctx, cluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status of cluster %s/%s: %w", cluster.Namespace, cluster.Name, err)
	}
	logf.FromContext(ctx).Info("Updated cluster usage status",
		"cluster", client.ObjectKeyFromObject(cluster).String(),
		"nodes", total.Nodes, "cpu", total.ResourceUsage.CPU, "memoryMiB", total.ResourceUsage.Memory)
	return ctrl.Result{RequeueAfter: usageSyncPeriod}, nil
}

// machineFlavorName reads the OpenStack flavor name from the machine's
// infrastructure machine.
func (r *ClusterReconciler) machineFlavorName(ctx context.Context, machine *clusterv1.Machine) (string, error) {
	ref := machine.Spec.InfrastructureRef
	if ref.Kind != openStackMachineKind || ref.APIGroup != capov1.GroupName {
		return "", fmt.Errorf("machine %s/%s has unsupported infrastructure reference %s/%s",
			machine.Namespace, machine.Name, ref.APIGroup, ref.Kind)
	}

	osm := &capov1.OpenStackMachine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: ref.Name}, osm); err != nil {
		return "", fmt.Errorf("get %s %s/%s: %w", ref.Kind, machine.Namespace, ref.Name, err)
	}
	if osm.Spec.Flavor == nil || *osm.Spec.Flavor == "" {
		return "", fmt.Errorf("openstackmachine %s/%s has no spec.flavor", machine.Namespace, ref.Name)
	}
	return *osm.Spec.Flavor, nil
}

func ownedByCAPICluster(cluster *batonv1.Cluster) bool {
	ownerRef := metav1.GetControllerOf(cluster)
	return ownerRef != nil &&
		ownerRef.Kind == clusterKind &&
		ownerRef.APIVersion == clusterv1.GroupVersion.String()
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&batonv1.Cluster{}).
		Named("cluster").
		Watches(&clusterv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(obj)}}
		})).
		Watches(&clusterv1.Machine{}, handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
			clusterName := obj.GetLabels()[clusterv1.ClusterNameLabel]
			if clusterName == "" {
				return nil
			}
			return []reconcile.Request{{NamespacedName: types.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      clusterName,
			}}}
		})).
		Complete(r)
}
