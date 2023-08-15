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

package common

import (
	"context"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	k8smcv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	mcv1alpha2 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha2"
	crdv1beta1 "antrea.io/antrea/pkg/apis/crd/v1beta1"
)

var DeleteEventPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		return false
	},
}

var StatusReadyPredicate = func(e event.UpdateEvent) bool {
	if e.ObjectOld == nil || e.ObjectNew == nil {
		return false
	}
	oldClusterSet := e.ObjectOld.(*mcv1alpha2.ClusterSet)
	newClusterSet := e.ObjectNew.(*mcv1alpha2.ClusterSet)
	if !reflect.DeepEqual(oldClusterSet.Status.Conditions, newClusterSet.Status.Conditions) {
		oldConditionSize := len(oldClusterSet.Status.Conditions)
		newConditionSize := len(newClusterSet.Status.Conditions)
		if oldConditionSize == 0 && newConditionSize > 0 && newClusterSet.Status.Conditions[0].Status == corev1.ConditionTrue {
			return true
		}
		if oldConditionSize > 0 && newConditionSize > 0 &&
			(oldClusterSet.Status.Conditions[0].Status == corev1.ConditionFalse || oldClusterSet.Status.Conditions[0].Status == corev1.ConditionUnknown) &&
			newClusterSet.Status.Conditions[0].Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func CleanUpResourcesCreatedByMC(ctx context.Context, mgrClient client.Client) error {
	var err error
	if err = cleanUpMCServicesAndServiceImports(ctx, mgrClient); err != nil {
		return err
	}
	if err = cleanUpReplicatedACNPs(ctx, mgrClient); err != nil {
		return err
	}
	if err = cleanUpLabelIdentities(ctx, mgrClient); err != nil {
		return err
	}
	if err = cleanUpClusterInfoImports(ctx, mgrClient); err != nil {
		return err
	}
	if err = cleanUpGateways(ctx, mgrClient); err != nil {
		return err
	}
	return nil
}

func cleanUpMCServicesAndServiceImports(ctx context.Context, mgrClient client.Client) error {
	svcImpList := &k8smcv1alpha1.ServiceImportList{}
	err := mgrClient.List(ctx, svcImpList, &client.ListOptions{})
	if err != nil {
		return err
	}
	for _, svcImp := range svcImpList.Items {
		svcImpTmp := svcImp
		mcsvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: svcImp.Namespace,
				Name:      ToMCResourceName(svcImp.Name),
			},
		}
		err = mgrClient.Delete(ctx, mcsvc, &client.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		err = mgrClient.Delete(ctx, &svcImpTmp, &client.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func cleanUpReplicatedACNPs(ctx context.Context, mgrClient client.Client) error {
	acnpList := &crdv1beta1.ClusterNetworkPolicyList{}
	if err := mgrClient.List(ctx, acnpList, &client.ListOptions{}); err != nil {
		return err
	}
	for _, acnp := range acnpList.Items {
		acnpTmp := acnp
		if metav1.HasAnnotation(acnp.ObjectMeta, AntreaMCACNPAnnotation) {
			err := mgrClient.Delete(ctx, &acnpTmp, &client.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
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
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func cleanUpClusterInfoImports(ctx context.Context, mgrClient client.Client) error {
	ciImpList := &mcv1alpha1.ClusterInfoImportList{}
	if err := mgrClient.List(ctx, ciImpList, &client.ListOptions{}); err != nil {
		return err
	}
	for _, ciImp := range ciImpList.Items {
		ciImpTmp := ciImp
		err := mgrClient.Delete(ctx, &ciImpTmp, &client.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func cleanUpGateways(ctx context.Context, mgrClient client.Client) error {
	gwList := &mcv1alpha1.GatewayList{}
	if err := mgrClient.List(ctx, gwList, &client.ListOptions{}); err != nil {
		return err
	}
	for _, gw := range gwList.Items {
		gwTmp := gw
		err := mgrClient.Delete(ctx, &gwTmp, &client.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}
