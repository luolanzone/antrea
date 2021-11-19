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

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	mcsscheme "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned/scheme"

	multiclusterv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	multiclustercontrollers "antrea.io/antrea/multicluster/controllers/multicluster"
	"antrea.io/antrea/multicluster/controllers/multicluster/clustermanager"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	mcsClient "antrea.io/antrea/multicluster/pkg/client/clientset/versioned"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const (
	timeout  = time.Second * 15
	interval = time.Second * 1
)

var (
	cfg                *rest.Config
	k8sClient          client.Client
	testEnv            *envtest.Environment
	antreaMcsCrdClient mcsClient.Interface
	k8sServerURL       string
	LocalClusterID     = "cluster-a"
	LeaderNamespace    = "leader-ns"
	clusterSetID       = "test-clusterset"
	testNamespace      = "testns"

	testNs = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespace,
		},
	}

	leaderNs = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: LeaderNamespace,
		},
	}
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	os.Setenv("KUBECONFIG", "/tmp/kube/config")
	By("bootstrapping test environment")
	useExistingCluster := true
	// binPath, err := os.Getwd()
	// os.Setenv("KUBEBUILDER_ASSETS", binPath+"/../../bin")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		UseExistingCluster:    &useExistingCluster,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = multiclusterv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = mcsscheme.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
	antreaMcsCrdClient, err = mcsClient.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())
	Log := klogr.New().WithName("controllers")
	k8sServerURL = testEnv.Config.Host

	ctx := context.Background()

	k8sClient.Create(ctx, leaderNs)
	k8sClient.Create(ctx, testNs)

	out, err := exec.Command("sudo", "../../ci/./export_token.sh").Output()
	token := strings.TrimRight(string(out), "\n")
	accessToken := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "access-token",
			Namespace: LeaderNamespace,
		},
		Data: map[string][]byte{
			"ca.crt": testEnv.Config.CAData,
			"token":  []byte(token),
		},
	}

	err = k8sClient.Create(ctx, accessToken, &client.CreateOptions{})
	if err != nil {
		Expect(err != nil && apierrors.IsAlreadyExists(err)).To(Equal(true))
	}
	time.Sleep(2 * time.Second)

	clustermanager.SetSecretClient(k8sClient)
	clusterSetReconciler := &multiclustercontrollers.ClusterSetReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Log:      Log,
		IsLeader: false,
	}
	err = clusterSetReconciler.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	remoteMgr := clustermanager.NewRemoteClusterManager("test-clusterset", Log, common.ClusterID(LocalClusterID))
	go remoteMgr.Start()
	_, err = clustermanager.NewRemoteCluster(common.ClusterID(LocalClusterID),
		"test-clusterset", k8sServerURL, "access-token",
		k8sManager.GetScheme(), Log, nil, &remoteMgr,
		LeaderNamespace, LeaderNamespace)
	Expect(err).ToNot(HaveOccurred())

	svcExportReconciler := multiclustercontrollers.NewServiceExportReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		&remoteMgr)
	err = svcExportReconciler.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// localMgr := clustermanager.NewLocalClusterManager(k8sClient, "leader-cluster", "leaderns-one", Log)
	// resExportReconciler := NewResourceExportReconciler(
	// 	k8sManager.GetClient(),
	// 	k8sManager.GetScheme(),
	// 	&localMgr)
	// err = resExportReconciler.SetupWithManager(k8sManager)
	// Expect(err).ToNot(HaveOccurred())

	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	k8sClient.Delete(context.TODO(), testNs)
	k8sClient.Delete(context.TODO(), leaderNs)
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
