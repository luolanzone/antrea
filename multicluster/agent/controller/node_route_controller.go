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

package controller

/*
set up route between general Node to Gateway Node
* watch local tunnelEndpoint event, Get the Gateway tunnel endpoint info
* Get ServiceCIDR/PodCIDR of other member clusters from remote tunnel Endpoints
* Configure Openflow rules to forward remote Service/Pod access to GW node
*/
