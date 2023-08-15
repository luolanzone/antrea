/*
Copyright 2023 Antrea Authors.

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

package member

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8smcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/pkg/apis/crd/v1beta1"
)

func cleanUpResourcesCreatedByMC(ctx context.Context, mgrClient client.Client) error {
	var err error
	if err = cleanUpMCServiceAndServiceImport(ctx, mgrClient); err != nil {
		return err
	}
	if err = cleanUpReplicatedACNP(ctx, mgrClient); err != nil {
		return err
	}
	if err = cleanUpLabelIdentities(ctx, mgrClient); err != nil {
		return err
	}
	if err = cleanUpClusterInfoImport(ctx, mgrClient); err != nil {
		return err
	}
	return nil
}

func cleanUpMCServiceAndServiceImport(ctx context.Context, mgrClient client.Client) error {
	svcImpList := &k8smcsv1alpha1.ServiceImportList{}
	err := mgrClient.List(ctx, svcImpList, &client.ListOptions{})
	if err != nil {
		return err
	}
	for _, svcImp := range svcImpList.Items {
		svcImpTmp := svcImp
		mcsvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: svcImp.Namespace,
				Name:      common.ToMCResourceName(svcImp.Name),
			},
		}
		err = mgrClient.Delete(ctx, mcsvc, &client.DeleteOptions{})
		if err == nil || (err != nil && apierrors.IsNotFound(err)) {
			err = mgrClient.Delete(ctx, &svcImpTmp, &client.DeleteOptions{})
			if err == nil || (err != nil && apierrors.IsNotFound(err)) {
				continue
			}
			return err
		}
		return err
	}
	return nil
}

func cleanUpReplicatedACNP(ctx context.Context, mgrClient client.Client) error {
	acnpList := &v1beta1.ClusterNetworkPolicyList{}
	if err := mgrClient.List(ctx, acnpList, &client.ListOptions{}); err != nil {
		return err
	}
	for _, acnp := range acnpList.Items {
		acnpTmp := acnp
		if metav1.HasAnnotation(acnp.ObjectMeta, common.AntreaMCACNPAnnotation) {
			err := mgrClient.Delete(ctx, &acnpTmp, &client.DeleteOptions{})
			if err == nil || (err != nil && apierrors.IsNotFound(err)) {
				continue
			}
			return err
		}
	}
	return nil
}

func cleanUpLabelIdentities(ctx context.Context, mgrClient client.Client) error {
	labelIdentityList := &mcv1alpha1.LabelIdentityList{}
	if err := mgrClient.List(ctx, labelIdentityList, &client.ListOptions{}); err != nil {
		return err
	}
	for _, labelIdt := range labelIdentityList.Items {
		labelIdtTmp := labelIdt
		err := mgrClient.Delete(ctx, &labelIdtTmp, &client.DeleteOptions{})
		if err == nil || (err != nil && apierrors.IsNotFound(err)) {
			continue
		}
		return err
	}
	return nil
}

func cleanUpClusterInfoImport(ctx context.Context, mgrClient client.Client) error {
	ciImpList := &mcv1alpha1.ClusterInfoImportList{}
	if err := mgrClient.List(ctx, ciImpList, &client.ListOptions{}); err != nil {
		return err
	}
	for _, ciImp := range ciImpList.Items {
		ciImpTmp := ciImp
		err := mgrClient.Delete(ctx, &ciImpTmp, &client.DeleteOptions{})
		if err == nil || (err != nil && apierrors.IsNotFound(err)) {
			continue
		}
		return err
	}
	return nil
}
