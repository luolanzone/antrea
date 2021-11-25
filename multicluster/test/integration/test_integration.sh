#!/usr/bin/env bash

# Copyright 2021 Antrea Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

mkdir -p /tmp/kube
chmod -R 777 /tmp/kube
cat /dev/null > /tmp/kube/config
chmod 777 /tmp/kube
export kubeconfig=/tmp/kube/config
kind create cluster --name=antrea-leader --kubeconfig=/tmp/kube/config
time sleep 5

kubectl create namespace leader-ns --kubeconfig=/tmp/kube/config
kubectl create -f config/integration/antrea-mc-member-access-sa.yml --kubeconfig=/tmp/kube/config
kubectl apply -f config/integration/token-secret.yml --kubeconfig=/tmp/kube/config
