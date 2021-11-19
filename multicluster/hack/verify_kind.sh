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

# The script creates and deletes kind testbeds. Kind testbeds may be created with
# docker images preloaded, antrea-cni preloaded, antrea-cni's encapsulation mode,
# and docker bridge network connecting to worker Node.

if ! which kind > /dev/null; then
    if [[ $(uname) == 'Darwin' || $(uname) == 'Linux' ]]; then
        curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.11.1/kind-$(uname)-amd64
        chmod +x ./kind
        sudo mv kind /usr/bin
    fi
fi
