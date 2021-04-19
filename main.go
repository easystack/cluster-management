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
	)

	flag.StringVar(&nameSpace, "resource-namespace", "eks", "The controller watch resources in which namespace")
	flag.StringVar(&metricsAddr, "metrics-addr", "0", "The address the metric endpoint binds to. default 0(means disable)")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	pollingPeriod := flag.Duration("polling-period", 5*time.Second, "The polling loop period.")
	openstackPeriod := flag.Duration("openstack-period", 25*time.Second, "The polling loop period.")
	syncdu := flag.Duration("sync-period", time.Second*30, "controller manager sync resource time duration")

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
		Port:               0,
		Namespace:          nameSpace,
	})
	if err != nil {
		panic(err)
	}

	cluster := controllers.NewCluster(k8mg, openstackMg, enableLeaderElection)
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
