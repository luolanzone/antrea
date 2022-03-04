/*
Copyright 2022 Antrea Authors.

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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const defaultServiceAccount = "antrea-mc-controller"

//+kubebuilder:webhook:path=/validate-multicluster-crd-antrea-io-v1alpha1-tunnelendpoint,mutating=false,failurePolicy=fail,sideEffects=None,groups=multicluster.crd.antrea.io,resources=tunnelendpoints,verbs=create;update,versions=v1alpha1,name=vtunnelendpoint.kb.io,admissionReviewVersions={v1,v1beta1}

type tunnelEndpointValidator struct {
	Client    client.Client
	decoder   *admission.Decoder
	namespace string
}

// Handle handles admission requests.
func (v *tunnelEndpointValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	// tunnelEndpoint := &mcv1alpha1.TunnelEndpoint{}
	// if err := v.decoder.Decode(req, tunnelEndpoint); err != nil {
	// 	klog.ErrorS(err, "error while decoding tunnelEndpoint")
	// 	return admission.Errored(http.StatusBadRequest, err)
	// }

	// ui := req.UserInfo
	// _, saName, err := serviceaccount.SplitUsername(ui.Username)
	// if err != nil {
	// 	klog.ErrorS(err, "error getting ServiceAccount name", "request", req)
	// 	return admission.Errored(http.StatusBadRequest, errors.New("only ServiceAccount is allowed to create TunnelEndpoint"))
	// }

	// if saName != defaultServiceAccount {
	// 	return admission.Denied("only Antrea Multicluster Controller ServiceAccount is allowed to create TunnelEndpoint")
	// }
	return admission.Allowed("")
}

func (v *tunnelEndpointValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}
