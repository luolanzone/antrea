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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var tunnelendpointlog = logf.Log.WithName("tunnelendpoint-resource")

func (r *TunnelEndpoint) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-multicluster-crd-antrea-io-v1alpha1-tunnelendpoint,mutating=true,failurePolicy=fail,sideEffects=None,groups=multicluster.crd.antrea.io,resources=tunnelendpoints,verbs=create;update,versions=v1alpha1,name=mtunnelendpoint.kb.io,admissionReviewVersions={v1,v1beta1}

var _ webhook.Defaulter = &TunnelEndpoint{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *TunnelEndpoint) Default() {
	tunnelendpointlog.Info("default", "name", r.Name)

	// TODO(user): fill in your defaulting logic.
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-multicluster-crd-antrea-io-v1alpha1-tunnelendpoint,mutating=false,failurePolicy=fail,sideEffects=None,groups=multicluster.crd.antrea.io,resources=tunnelendpoints,verbs=create;update,versions=v1alpha1,name=vtunnelendpoint.kb.io,admissionReviewVersions={v1,v1beta1}

var _ webhook.Validator = &TunnelEndpoint{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *TunnelEndpoint) ValidateCreate() error {
	tunnelendpointlog.Info("validate create", "name", r.Name)

	// TODO(user): fill in your validation logic upon object creation.
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *TunnelEndpoint) ValidateUpdate(old runtime.Object) error {
	tunnelendpointlog.Info("validate update", "name", r.Name)

	// TODO(user): fill in your validation logic upon object update.
	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *TunnelEndpoint) ValidateDelete() error {
	tunnelendpointlog.Info("validate delete", "name", r.Name)

	// TODO(user): fill in your validation logic upon object deletion.
	return nil
}
