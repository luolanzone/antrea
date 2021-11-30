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
	"time"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8smcsapi "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var _ = Describe("ResourceImport controller", func() {

	ctx := context.Background()
	It("ResourceImport ", func() {
		By("By update Endpoint in existing and nonexisting ResourceImport")
		epSubset := []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP:       "192.168.17.11",
						Hostname: "pod1",
					},
				},
				Ports: []corev1.EndpointPort{
					{
						Name:     "http",
						Port:     80,
						Protocol: corev1.ProtocolTCP,
					},
				},
			},
		}
		endpointResImport := &mcsv1alpha1.ResourceImport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "leader-ns",
				Name:      "endpointresourceimport",
			},
			Spec: mcsv1alpha1.ResourceImportSpec{
				Namespace: "testns",
				Name:      "nginx",
				Kind:      "Endpoints",
				Endpoints: &mcsv1alpha1.EndpointsImport{
					Subsets: epSubset,
				},
			},
		}
		latestResImportNoRes := &mcsv1alpha1.ResourceImport{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Namespace: "leader-ns",
			Name:      "endpointresourceimport",
		}, latestResImportNoRes)).To(HaveOccurred())
		Expect(k8sClient.Create(ctx, endpointResImport)).Should(Succeed())
		newSubsets := []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP:       "192.168.17.12",
						Hostname: "pod2",
					},
				},
				Ports: []corev1.EndpointPort{
					{
						Name:     "http",
						Port:     8080,
						Protocol: corev1.ProtocolTCP,
					},
				},
			},
		}
		latestResImport := &mcsv1alpha1.ResourceImport{}
		resImportName := types.NamespacedName{
			Namespace: "leader-ns",
			Name:      "endpointresourceimport",
		}
		err := k8sClient.Get(ctx, resImportName, latestResImport)
		Expect(err).ToNot(HaveOccurred())

		latestResImport.Spec.Endpoints.Subsets = newSubsets
		Expect(k8sClient.Update(ctx, latestResImport)).Should(Succeed())
		time.Sleep(2 * time.Second)
		epResImport := &mcsv1alpha1.ResourceImport{}

		Eventually(func() bool {
			epResImport, err = antreaMcsCrdClient.MulticlusterV1alpha1().ResourceImports(LeaderNamespace).Get(ctx, "endpointresourceimport", metav1.GetOptions{})
			return err == nil
		}, timeout, interval).Should(BeTrue())
		Expect(epResImport.Spec.Kind).Should(Equal("Endpoints"))
		Expect(epResImport.Spec.Endpoints.Subsets).Should(Equal(newSubsets))

		toupdateResImport := &mcsv1alpha1.ResourceImport{}
		err = k8sClient.Get(ctx, resImportName, toupdateResImport)
		Expect(err).ToNot(HaveOccurred())
		toupdateResImport.Spec.Endpoints.Subsets = epSubset
		Expect(k8sClient.Delete(ctx, epResImport)).Should(Succeed())
		Expect(k8sClient.Update(ctx, toupdateResImport)).To(HaveOccurred())

	})
	It("Should get a deleted ResourceImport", func() {
		By("By get a deleted ResourceImport")
		serviceimportResImport := &mcsv1alpha1.ResourceImport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "leader-ns",
				Name:      "serviceimportresourceimport",
			},
			Spec: mcsv1alpha1.ResourceImportSpec{
				Namespace: "leader-ns",
				Name:      "nginx",
				Kind:      "ServiceImport",
				ServiceImport: &k8smcsapi.ServiceImport{
					Spec: k8smcsapi.ServiceImportSpec{
						Ports: []k8smcsapi.ServicePort{
							{
								Name:     "http",
								Protocol: corev1.ProtocolTCP,
								Port:     80,
							},
						},
						Type: k8smcsapi.ClusterSetIP,
					},
				},
			},
		}
		latestResImportNoRes := &mcsv1alpha1.ResourceImport{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Namespace: "leader-ns",
			Name:      "serviceimportresourceimport",
		}, latestResImportNoRes)).To(HaveOccurred())
		Expect(k8sClient.Create(ctx, serviceimportResImport)).Should(Succeed())
		newPorts := []k8smcsapi.ServicePort{
			{
				Name:     "http",
				Protocol: corev1.ProtocolTCP,
				Port:     8080,
			},
		}

		latestResImport := &mcsv1alpha1.ResourceImport{}
		resImportName := types.NamespacedName{
			Namespace: "leader-ns",
			Name:      "serviceimportresourceimport",
		}
		err := k8sClient.Get(ctx, resImportName, latestResImport)
		Expect(err).ToNot(HaveOccurred())
		latestResImport.Spec.ServiceImport.Spec.Ports = newPorts
		Expect(k8sClient.Update(ctx, latestResImport)).Should(Succeed())

		time.Sleep(2 * time.Second)
		epResImport := &mcsv1alpha1.ResourceImport{}

		Eventually(func() bool {
			epResImport, err = antreaMcsCrdClient.MulticlusterV1alpha1().ResourceImports(LeaderNamespace).Get(ctx, "serviceimportresourceimport", metav1.GetOptions{})
			return err == nil
		}, timeout, interval).Should(BeTrue())
		Expect(epResImport.Spec.Kind).Should(Equal("ServiceImport"))
		Expect(epResImport.Spec.ServiceImport.Spec.Ports).Should(Equal(newPorts))

		toupdateResImport := &mcsv1alpha1.ResourceImport{}
		err = k8sClient.Get(ctx, resImportName, toupdateResImport)
		Expect(err).ToNot(HaveOccurred())
		toupdateResImport.Spec.ServiceImport.Spec.Ports = []k8smcsapi.ServicePort{
			{
				Name:     "http",
				Protocol: corev1.ProtocolTCP,
				Port:     80,
			},
		}
		Expect(k8sClient.Delete(ctx, epResImport)).Should(Succeed())
		Expect(k8sClient.Update(ctx, toupdateResImport)).To(HaveOccurred())

	})
})
