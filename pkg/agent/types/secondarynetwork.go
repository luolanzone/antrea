// Copyright 2026 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package types

import "time"

// SecondaryNetworkReadyNotifier is implemented by the secondary network controller.
// It allows the CNI server to wait for secondary network interfaces to be configured
// before returning success for a Pod ADD request, matching Multus-like behavior where
// Pod creation fails if secondary interfaces cannot be set up.
type SecondaryNetworkReadyNotifier interface {
	// RegisterPodSecondaryNetworkWait registers a pending result channel for the given
	// containerID. The CNI server calls this before starting to wait, and the secondary
	// network controller signals the channel when configuration completes (or fails).
	RegisterPodSecondaryNetworkWait(containerID string)
	// WaitForPodSecondaryNetworkReady blocks until all secondary network interfaces for
	// the Pod identified by containerID have been configured, or until the timeout
	// elapses. An error is returned if any secondary interface failed to be configured
	// or the timeout was exceeded.
	WaitForPodSecondaryNetworkReady(containerID string, timeout time.Duration) error
	// CancelPodSecondaryNetworkWait cancels the pending wait for a given containerID,
	// cleaning up any allocated state. This is called on CNI Del or rollback.
	CancelPodSecondaryNetworkWait(containerID string)
}
