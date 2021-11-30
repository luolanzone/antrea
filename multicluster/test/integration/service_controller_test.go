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

	"antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Service controller", func() {
	svcSpec := corev1.ServiceSpec{
		Ports: svcPorts,
	}
	ctx := context.Background()
	It("Should clean up MCS Service if no corresponding ResourceImport in leader cluster", func() {
		By("By claim a service without ResourceImport in leader cluster")
		svcToDelete := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "nginx",
				Namespace:   testNamespace,
				Labels:      map[string]string{common.AntreaMCSAutoGenAnnotation: "true"},
				Annotations: map[string]string{common.SourceImportAnnotation: "doesnotexist"},
			},
			Spec: svcSpec,
		}

		svcToDeleteNamespacedName := types.NamespacedName{
			Namespace: svcToDelete.Namespace,
			Name:      svcToDelete.Name,
		}
		Expect(k8sClient.Create(ctx, svcToDelete)).Should(Succeed())

		Eventually(func() bool {
			latestSvc := &corev1.Service{}
			err := k8sClient.Get(ctx, svcToDeleteNamespacedName, latestSvc)
			return apierrors.IsNotFound(err)
		}, timeout, interval).Should(BeTrue())

	})
	It("Should not clean up MCS Service if there is corresponding ResourceImport in leader cluster", func() {
		By("By claim a service and a corresponding ResourceImport in leader cluster")
		resImport := &v1alpha1.ResourceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "resourceimportexist",
				Namespace: LeaderNamespace,
			},
		}

		_, err := antreaMcsCrdClient.MulticlusterV1alpha1().ResourceImports(LeaderNamespace).Create(ctx, resImport, metav1.CreateOptions{})
		Expect(err == nil).Should(BeTrue())

		svcNoDelete := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "nginxtodelete",
				Namespace:   testNamespace,
				Labels:      map[string]string{common.AntreaMCSAutoGenAnnotation: "true"},
				Annotations: map[string]string{common.SourceImportAnnotation: "resourceimportexist"},
			},
			Spec: svcSpec,
		}

		svcNoDeleteNamespacedName := types.NamespacedName{
			Namespace: svcNoDelete.Namespace,
			Name:      svcNoDelete.Name,
		}
		Expect(k8sClient.Create(ctx, svcNoDelete)).Should(Succeed())

		Eventually(func() bool {
			latestSvc := &corev1.Service{}
			err := k8sClient.Get(ctx, svcNoDeleteNamespacedName, latestSvc)
			return err == nil
		}, timeout, interval).Should(BeTrue())

	})
})
