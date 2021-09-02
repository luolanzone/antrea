/*
Copyright 2021 Antrea Authors.

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
	"context"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	k8smcsversioned "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsscheme "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned/scheme"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	mcscontrollers "antrea.io/antrea/multicluster/controllers/multicluster"
	mcsClient "antrea.io/antrea/multicluster/pkg/client/clientset/versioned"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(mcsscheme.AddToScheme(scheme))
	utilruntime.Must(mcsv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var member bool
	var leader bool
	var probeAddr string
	var config string
	var namespace string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&member, "member", true, "Join the ClusterSet as a member")
	flag.BoolVar(&leader, "leader", false, "Join the ClusterSet as a leader")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&config, "config", "", "Configuration file path.")
	flag.StringVar(&namespace, "namespace", "kube-system", "Controller's default Namespace")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	k8sConfig := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(k8sConfig, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "6536456a.crd.antrea.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := context.Background()
	crdClient := mcsClient.NewForConfigOrDie(k8sConfig)
	memberAnnounces, err := crdClient.MulticlusterV1alpha1().MemberClusterAnnounces(namespace).List(ctx, v1.ListOptions{})
	if err != nil {
		setupLog.Error(err, "unable to get MemberClusterAnnounce list")
		os.Exit(1)
	}
	if len(memberAnnounces.Items) == 0 {
		setupLog.Error(err, "unable to get MemberClusterAnnounce information")
		os.Exit(1)
	}
	// suppose there is only one MemberAnnounce
	// will Saas support a member to join multiple ClusterSet?
	m := memberAnnounces.Items[0]
	clusterSet, err := crdClient.MulticlusterV1alpha1().ClusterSets(namespace).Get(ctx, m.ClusterSetID, v1.GetOptions{})
	if err != nil {
		setupLog.Error(err, "fail to get ClusterSet information")
		os.Exit(1)
	}

	var leaderCluster mcsv1alpha1.MemberCluster
	var leaderFound bool
	for _, l := range clusterSet.Spec.Leaders {
		if l.ClusterID == m.LeaderClusterID {
			leaderCluster = l
			leaderFound = true
			break
		}
	}

	if !leaderFound {
		setupLog.Error(err, "fail to get Leader Cluster information")
		os.Exit(1)
	}

	mcscontrollers.LeaderNameSpace = clusterSet.Spec.Namespace
	mcscontrollers.LocalClusterID = m.ClusterID
	// read secret in local cluster for leader cluster access
	kubeClient, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	accessInfo, err := kubeClient.CoreV1().Secrets(namespace).Get(ctx, leaderCluster.Secret, v1.GetOptions{})
	if err != nil {
		klog.Fatalf("Error getting secret %s : %s", leaderCluster.Secret, err.Error())
	}
	tlsClientConfig := rest.TLSClientConfig{}
	tlsClientConfig.CAData = accessInfo.Data["ca.crt"]
	bearerToken := accessInfo.Data["token"]
	leaderConfig := rest.Config{
		Host:            leaderCluster.Server,
		TLSClientConfig: tlsClientConfig,
		BearerToken:     string(bearerToken),
	}

	if err = (&mcscontrollers.ClusterClaimReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterClaim")
		os.Exit(1)
	}
	if err = (&mcscontrollers.MemberClusterAnnounceReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MemberClusterAnnounce")
		os.Exit(1)
	}
	if err = (&mcscontrollers.ClusterSetReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterSet")
		os.Exit(1)
	}
	if err = (&mcscontrollers.ResourceExportFilterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ResourceExportFilter")
		os.Exit(1)
	}
	if err = (&mcscontrollers.ResourceImportFilterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ResourceImportFilter")
		os.Exit(1)
	}
	if err = (&mcsv1alpha1.ClusterClaim{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ClusterClaim")
		os.Exit(1)
	}
	if err = (&mcsv1alpha1.ClusterSet{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ClusterSet")
		os.Exit(1)
	}
	if err = (&mcsv1alpha1.MemberClusterAnnounce{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "MemberClusterAnnounce")
		os.Exit(1)
	}
	k8smcsCrdClient, err := k8smcsversioned.NewForConfig(k8sConfig)
	if err != nil {
		klog.Fatalf("Error building K8s MCS clientset: %s", err.Error())
	}
	antreamcsLeaderCrdClient, err := mcsClient.NewForConfig(&leaderConfig)
	if err != nil {
		klog.Fatalf("Error building Antrea MCS leader clientset: %s", err.Error())
	}
	if member {
		svcExportReconciler := mcscontrollers.NewServiceExportReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			kubeClient,
			k8smcsCrdClient,
			antreamcsLeaderCrdClient)
		if err = svcExportReconciler.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ServiceExport")
			os.Exit(1)
		}
	}

	if leader {
		antreamcsLocalCrdClient, err := mcsClient.NewForConfig(k8sConfig)
		if err != nil {
			klog.Fatalf("Error building Antrea MCS leader clientset: %s", err.Error())
		}
		resExportReconciler := mcscontrollers.NewResourceExportReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			antreamcsLocalCrdClient,
		)
		if err = resExportReconciler.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ResourceExport")
			os.Exit(1)
		}
	}

	if err = (&mcscontrollers.ResourceImportReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ResourceImport")
		os.Exit(1)
	}
	if err = (&mcsv1alpha1.ResourceImport{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ResourceImport")
		os.Exit(1)
	}
	if err = (&mcsv1alpha1.ResourceExport{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "ResourceExport")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
