# Copyright 2026 Antrea Authors
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

# Build from the repository root. Supply a reviewed builder digest in production.
ARG GO_IMAGE=golang:1.27
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY hack/ipam-recovery ./hack/ipam-recovery
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ipam-recovery ./hack/ipam-recovery

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /ipam-recovery /ipam-recovery
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/ipam-recovery"]
