/*
Copyright 2020 easystack.

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
	"reflect"

	v1 "github.com/cluster-management/pkg/api/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	cli "sigs.k8s.io/controller-runtime/pkg/client"

	klog "k8s.io/klog/v2"
)

// Reconciler reconciles a VirtualMachine object
type Reconciler struct {
	cli.Client
	ctx    context.Context
	server *Operate
}

func NewController(mgr ctrl.Manager, server *Operate) *Reconciler {
	vmm := &Reconciler{
		Client: mgr.GetClient(),
		ctx:    context.Background(),
		server: server,
	}
	err := vmm.probe(mgr)
	if err != nil {
		panic(err)
	}
	return vmm
}

func (r *Reconciler) probe(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Cluster{}).Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		vm     v1.Cluster
		bctx   = context.Background()
		err    error
		nsname = req.NamespacedName.String()
	)

	err = r.Get(ctx, req.NamespacedName, &vm)
	if err != nil {
		if apierrs.IsNotFound(err) {
			klog.Infof("object %s had deleted", nsname)
			r.server.Delete(nsname)
			return ctrl.Result{}, nil
		}
		klog.Errorf("get object %s failed:%s", nsname, err)
	}
	newvmobj := &vm

	if newvmobj.DeletionTimestamp != nil {
		klog.V(2).Infof("object %s is deleting", nsname)
		//if processs failed, should block
		r.server.Delete(nsname)
	} else {
		err = r.server.Process(newvmobj, nsname)
		if _, ok := err.(RemoveCrErr); ok {
			klog.Infof("start remove cr %v", nsname)
			return ctrl.Result{}, r.Delete(bctx, &vm)
		}
	}

	err = r.doUpdateVmCrdStatus(req.NamespacedName, newvmobj)
	if err != nil {
		klog.Errorf("update %v failed:%v", req.NamespacedName, err)
	}
	return ctrl.Result{}, err
}

func (r *Reconciler) doUpdateVmCrdStatus(nsname types.NamespacedName, newcl *v1.Cluster) error {
	var isup bool
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var (
			original = &v1.Cluster{}
		)
		isup = false

		if err := r.Get(r.ctx, nsname, original); err != nil {
			klog.Errorf("get object %s failed:%v", nsname.String(), err)
			return err
		}
		if !reflect.DeepEqual(original.Spec, newcl.Spec) {
			newcl.Spec.DeepCopyInto(&original.Spec)
			isup = true
		}
		if !reflect.DeepEqual(original.Status, newcl.Status) {
			newcl.Status.DeepCopyInto(&original.Status)
			isup = true
		}
		if isup {
			return r.Update(r.ctx, original)
		}
		return nil
	})
}
