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
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	batonv1 "github.com/cluster-management/api/v1"
	"github.com/cluster-management/internal/openstack"
)

type fakeFlavorLister struct {
	flavors map[string]openstack.Flavor
	err     error
}

func (f *fakeFlavorLister) ListFlavors(_ context.Context) (map[string]openstack.Flavor, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.flavors, nil
}

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(batonv1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(capov1.AddToScheme(scheme))
	return scheme
}

func capiClusterObj(name string) *clusterv1.Cluster {
	return &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "launcher",
			UID:       types.UID("capi-uid-" + name),
		},
	}
}

func machineObj(name, osmName string) *clusterv1.Machine {
	return &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "launcher",
			Labels:    map[string]string{clusterv1.ClusterNameLabel: "m2"},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "m2",
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: capov1.GroupName,
				Kind:     "OpenStackMachine",
				Name:     osmName,
			},
		},
	}
}

func osmObj(name, flavor string) *capov1.OpenStackMachine {
	return &capov1.OpenStackMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "launcher"},
		Spec:       capov1.OpenStackMachineSpec{Flavor: new(flavor)},
	}
}

func TestClusterUsageSync(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	resolver := &fakeFlavorLister{flavors: map[string]openstack.Flavor{
		"4C-8G": {Name: "4C-8G", CPU: 4, MemoryMiB: 8192},
	}}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&batonv1.Cluster{}).WithObjects(
		capiClusterObj("m2"),
		machineObj("m2-etcd-qhp5m-dzlzh", "m2-etcd-osm"),
		machineObj("m2-worker-g5grv-dwsj4", "m2-worker-osm"),
		osmObj("m2-etcd-osm", "4C-8G"),
		osmObj("m2-worker-osm", "4C-8G"),
	).Build()
	reconciler := &ClusterReconciler{Client: fakeClient, Scheme: scheme, FlavorLister: resolver}
	key := types.NamespacedName{Namespace: "launcher", Name: "m2"}

	// First reconcile ensures the Cluster object exists.
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	cluster := &batonv1.Cluster{}
	if err := fakeClient.Get(ctx, key, cluster); err != nil {
		t.Fatalf("get created cluster: %v", err)
	}
	ownerRef := metav1.GetControllerOf(cluster)
	if ownerRef == nil || ownerRef.Kind != clusterKind || ownerRef.APIVersion != clusterv1.GroupVersion.String() {
		t.Fatalf("ownerRef = %+v, want controller ref to %s", ownerRef, clusterv1.GroupVersion.String())
	}

	// Second reconcile syncs the node usage.
	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if result.RequeueAfter != usageSyncPeriod {
		t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, usageSyncPeriod)
	}

	if err := fakeClient.Get(ctx, key, cluster); err != nil {
		t.Fatalf("get cluster: %v", err)
	}
	if len(cluster.Status.NodeInfo) != 2 {
		t.Fatalf("nodes = %+v, want 2 entries", cluster.Status.NodeInfo)
	}
	wantNames := []string{"m2-etcd-qhp5m-dzlzh", "m2-worker-g5grv-dwsj4"}
	for i, node := range cluster.Status.NodeInfo {
		if node.Name != wantNames[i] {
			t.Errorf("nodes[%d].Name = %q, want %q", i, node.Name, wantNames[i])
		}
		if node.Flavor != "4C-8G" || node.ResourceUsage.CPU != 4 || node.ResourceUsage.Memory != 8192 {
			t.Errorf("nodes[%d] = %+v", i, node)
		}
	}
	total := cluster.Status.ClusterInfo
	if total.Nodes != 2 || total.ResourceUsage.CPU != 8 || total.ResourceUsage.Memory != 16384 {
		t.Errorf("clusterInfo = %+v, want 2 nodes, 8 cpu, 16384 MiB", total)
	}
}

func TestNoClusterWithoutCAPICluster(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&batonv1.Cluster{}).Build()
	reconciler := &ClusterReconciler{Client: fakeClient, Scheme: scheme, FlavorLister: &fakeFlavorLister{}}

	key := types.NamespacedName{Namespace: "launcher", Name: "orphan"}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	cluster := &batonv1.Cluster{}
	if err := fakeClient.Get(ctx, key, cluster); !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound for orphan cluster, got %v", err)
	}
}

func TestManualClusterIsSkipped(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	manual := &batonv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "manual", Namespace: "launcher"}}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&batonv1.Cluster{}).WithObjects(manual).Build()
	reconciler := &ClusterReconciler{Client: fakeClient, Scheme: scheme, FlavorLister: &fakeFlavorLister{}}

	key := types.NamespacedName{Namespace: "launcher", Name: "manual"}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	cluster := &batonv1.Cluster{}
	if err := fakeClient.Get(ctx, key, cluster); err != nil {
		t.Fatalf("get cluster: %v", err)
	}
	if len(cluster.Status.NodeInfo) != 0 {
		t.Errorf("status = %+v, want untouched", cluster.Status)
	}
}

func TestResolveFailureKeepsStatus(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&batonv1.Cluster{}).WithObjects(
		capiClusterObj("m2"),
		machineObj("m2-etcd-qhp5m-dzlzh", "m2-etcd-osm"),
		osmObj("m2-etcd-osm", "4C-8G"),
	).Build()
	reconciler := &ClusterReconciler{
		Client:       fakeClient,
		Scheme:       scheme,
		FlavorLister: &fakeFlavorLister{err: errors.New("openstack unreachable")},
	}
	key := types.NamespacedName{Namespace: "launcher", Name: "m2"}

	// Ensures the Cluster object.
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err == nil {
		t.Fatal("expected error from flavor resolution failure")
	}

	cluster := &batonv1.Cluster{}
	if err := fakeClient.Get(ctx, key, cluster); err != nil {
		t.Fatalf("get cluster: %v", err)
	}
	if len(cluster.Status.NodeInfo) != 0 {
		t.Errorf("nodes = %+v, want empty on failure", cluster.Status.NodeInfo)
	}
}
