// Copyright 2024 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	"context"

	v1alpha1 "antrea.io/antrea/pkg/apis/crd/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakePacketCaptures implements PacketCaptureInterface
type FakePacketCaptures struct {
	Fake *FakeCrdV1alpha1
}

var packetcapturesResource = v1alpha1.SchemeGroupVersion.WithResource("packetcaptures")

var packetcapturesKind = v1alpha1.SchemeGroupVersion.WithKind("PacketCapture")

// Get takes name of the packetCapture, and returns the corresponding packetCapture object, and an error if there is any.
func (c *FakePacketCaptures) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.PacketCapture, err error) {
	emptyResult := &v1alpha1.PacketCapture{}
	obj, err := c.Fake.
		Invokes(testing.NewRootGetActionWithOptions(packetcapturesResource, name, options), emptyResult)
	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.PacketCapture), err
}

// List takes label and field selectors, and returns the list of PacketCaptures that match those selectors.
func (c *FakePacketCaptures) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.PacketCaptureList, err error) {
	emptyResult := &v1alpha1.PacketCaptureList{}
	obj, err := c.Fake.
		Invokes(testing.NewRootListActionWithOptions(packetcapturesResource, packetcapturesKind, opts), emptyResult)
	if obj == nil {
		return emptyResult, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.PacketCaptureList{ListMeta: obj.(*v1alpha1.PacketCaptureList).ListMeta}
	for _, item := range obj.(*v1alpha1.PacketCaptureList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested packetCaptures.
func (c *FakePacketCaptures) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchActionWithOptions(packetcapturesResource, opts))
}

// Create takes the representation of a packetCapture and creates it.  Returns the server's representation of the packetCapture, and an error, if there is any.
func (c *FakePacketCaptures) Create(ctx context.Context, packetCapture *v1alpha1.PacketCapture, opts v1.CreateOptions) (result *v1alpha1.PacketCapture, err error) {
	emptyResult := &v1alpha1.PacketCapture{}
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateActionWithOptions(packetcapturesResource, packetCapture, opts), emptyResult)
	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.PacketCapture), err
}

// Update takes the representation of a packetCapture and updates it. Returns the server's representation of the packetCapture, and an error, if there is any.
func (c *FakePacketCaptures) Update(ctx context.Context, packetCapture *v1alpha1.PacketCapture, opts v1.UpdateOptions) (result *v1alpha1.PacketCapture, err error) {
	emptyResult := &v1alpha1.PacketCapture{}
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateActionWithOptions(packetcapturesResource, packetCapture, opts), emptyResult)
	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.PacketCapture), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakePacketCaptures) UpdateStatus(ctx context.Context, packetCapture *v1alpha1.PacketCapture, opts v1.UpdateOptions) (result *v1alpha1.PacketCapture, err error) {
	emptyResult := &v1alpha1.PacketCapture{}
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateSubresourceActionWithOptions(packetcapturesResource, "status", packetCapture, opts), emptyResult)
	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.PacketCapture), err
}

// Delete takes name of the packetCapture and deletes it. Returns an error if one occurs.
func (c *FakePacketCaptures) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteActionWithOptions(packetcapturesResource, name, opts), &v1alpha1.PacketCapture{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakePacketCaptures) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionActionWithOptions(packetcapturesResource, opts, listOpts)

	_, err := c.Fake.Invokes(action, &v1alpha1.PacketCaptureList{})
	return err
}

// Patch applies the patch and returns the patched packetCapture.
func (c *FakePacketCaptures) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.PacketCapture, err error) {
	emptyResult := &v1alpha1.PacketCapture{}
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceActionWithOptions(packetcapturesResource, name, pt, data, opts, subresources...), emptyResult)
	if obj == nil {
		return emptyResult, err
	}
	return obj.(*v1alpha1.PacketCapture), err
}
