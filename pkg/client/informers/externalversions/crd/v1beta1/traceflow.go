// Copyright 2023 Antrea Authors
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

// Code generated by informer-gen. DO NOT EDIT.

package v1beta1

import (
	"context"
	time "time"

	crdv1beta1 "antrea.io/antrea/pkg/apis/crd/v1beta1"
	versioned "antrea.io/antrea/pkg/client/clientset/versioned"
	internalinterfaces "antrea.io/antrea/pkg/client/informers/externalversions/internalinterfaces"
	v1beta1 "antrea.io/antrea/pkg/client/listers/crd/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// TraceflowInformer provides access to a shared informer and lister for
// Traceflows.
type TraceflowInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1beta1.TraceflowLister
}

type traceflowInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
}

// NewTraceflowInformer constructs a new informer for Traceflow type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewTraceflowInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredTraceflowInformer(client, resyncPeriod, indexers, nil)
}

// NewFilteredTraceflowInformer constructs a new informer for Traceflow type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredTraceflowInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.CrdV1beta1().Traceflows().List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.CrdV1beta1().Traceflows().Watch(context.TODO(), options)
			},
		},
		&crdv1beta1.Traceflow{},
		resyncPeriod,
		indexers,
	)
}

func (f *traceflowInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredTraceflowInformer(client, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *traceflowInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&crdv1beta1.Traceflow{}, f.defaultInformer)
}

func (f *traceflowInformer) Lister() v1beta1.TraceflowLister {
	return v1beta1.NewTraceflowLister(f.Informer().GetIndexer())
}
