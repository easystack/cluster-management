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

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tgpkg "github.com/cluster-management/pkg/tag"

	ecnsv1 "github.com/cluster-management/pkg/api/v1"
	"github.com/cluster-management/pkg/controllers"
	"github.com/cluster-management/pkg/k8s"
	oppkg "github.com/cluster-management/pkg/openstack"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = ecnsv1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var (
		leaderid             = "cluster.manager"
		metricsAddr          string
		enableLeaderElection bool
		nameSpace            string
		createTagUrl         string
		crRemoveDelay        time.Duration
	)

	flag.StringVar(&nameSpace, "namespace", "", "The controller watch resources in which namespace(default all)")
	flag.StringVar(&metricsAddr, "metrics-addr", "0", "The address the metric endpoint binds to. default 0(means disable)")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&createTagUrl, "createtagurl", "http://eks-dashboard-api.eks.svc.cluster.local/api/container_infra/create_tag/",
		"The url in eks-dashboard-api for auto create tag")
	flag.DurationVar(&crRemoveDelay, "cr-remove-delay-interval", 30*time.Minute,
		"Number of seconds after the cluster is not found in Magnum before CR is deleted")
	pollingPeriod := flag.Duration("polling-period", 13*time.Second, "The polling loop period.")
	openstackPeriod := flag.Duration("openstack-period", 23*time.Second, "The polling loop period.")
	tagPeriod := flag.Duration("tag-period", 900*time.Second, "The polling loop period.")
	syncdu := flag.Duration("sync-period", 30*time.Second, "Controller manager sync resource time duration")

	klog.InitFlags(flag.CommandLine)
	flag.Parse()

	openstackMg := oppkg.NewOpMgr(*openstackPeriod)
	k8mg := k8s.NewManage(*pollingPeriod, 5, func(_ string) (string, error) {
		return openstackMg.NewToken()
	})

	newleaderid := fmt.Sprintf("%s.%s", leaderid, nameSpace)
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		SyncPeriod:         syncdu,
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   newleaderid,
		Namespace:          nameSpace,
	})
	if err != nil {
		panic(err)
	}

	tagmg := tgpkg.NewTagMgr(*tagPeriod, createTagUrl)

	cluster := controllers.NewCluster(k8mg, openstackMg, tagmg, enableLeaderElection, crRemoveDelay)
	controllers.NewController(mgr, cluster)
	err = mgr.Add(cluster)
	if err != nil {
		panic(err)
	}
	klog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Errorf("start manager failed:%v", err)
		os.Exit(1)
	}
}
