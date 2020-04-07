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
	"github.com/cluster-management/pkg/k8s"
	osservice "github.com/cluster-management/pkg/openstack"
	"github.com/gophercloud/gophercloud/openstack"
	"golang.org/x/net/context"
	"os"

	eosv1 "github.com/cluster-management/pkg/api/v1"
	"github.com/cluster-management/pkg/controllers"
	"k8s.io/apimachinery/pkg/runtime"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = eosv1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8899", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	ctrl.SetLogger(zap.Logger(true))

	// set env for test
	os.Setenv("OS_AUTH_URL", "http://keystone-api.openstack.svc.cluster.local/v3")
	os.Setenv("OS_PROJECT_NAME", "admin")
	os.Setenv("OS_PROJECT_DOMAIN_NAME", "Default")
	os.Setenv("OS_USER_DOMAIN_NAME", "Default")
	os.Setenv("OS_DOMAIN_NAME", "Default")
	os.Setenv("OS_USERNAME", "admin")
	os.Setenv("OS_PASSWORD", "Admin@ES20!8")

	// get ECS cloud admin credential info from env
	opts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		fmt.Print(err)
		setupLog.Error(err, "Failed to start since missing necessary openstack environment:")
		os.Exit(1)
	}

	// Get keystone token to access kubernetes API
	ctx := context.WithValue(context.Background(), "logger", setupLog)
	osClient := osservice.OSService{Opts: &opts}
	token, _ := osClient.GetKeystoneToken(ctx)

	k8sReconcile := k8s.KService{Token: token}
	k8sPolling := k8s.KService{Token: token}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
		Port:               9443,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	rc := controllers.NewEosClusterReconciler(mgr.GetClient(), ctrl.Log.WithName("EosCluster"), &opts, &osClient, &k8sReconcile)

	polling := controllers.NewEosClusterReconciler(mgr.GetClient(), ctrl.Log.WithName("EosCluster"), &opts, &osClient, &k8sPolling)
	go polling.PollingClusterInfo()

	err = rc.SetupWithManager(mgr)
	if err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EosCluster")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder
	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
