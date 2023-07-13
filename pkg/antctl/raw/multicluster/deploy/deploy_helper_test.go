// Copyright 2022 Antrea Authors
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

package deploy

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/restmapper"

	mcscheme "antrea.io/antrea/pkg/antctl/raw/multicluster/scheme"
)

func TestGenerateManifests(t *testing.T) {
	tests := []struct {
		name              string
		role              string
		version           string
		expectedManifests []string
		expectedErr       string
	}{
		{
			name:    "generate latest leader manifests",
			role:    "leader",
			version: "latest",
			expectedManifests: []string{
				"https://raw.githubusercontent.com/antrea-io/antrea/main/multicluster/build/yamls/antrea-multicluster-leader-global.yml",
				"https://raw.githubusercontent.com/antrea-io/antrea/main/multicluster/build/yamls/antrea-multicluster-leader-namespaced.yml",
			},
		},
		{
			name:    "generate latest member manifests",
			role:    "member",
			version: "latest",
			expectedManifests: []string{
				"https://raw.githubusercontent.com/antrea-io/antrea/main/multicluster/build/yamls/antrea-multicluster-member.yml",
				"https://raw.githubusercontent.com/antrea-io/antrea/main/multicluster/build/yamls/antrea-multicluster-member-global.yml",
			},
		},
		{
			name:    "generate versioned leader manifests",
			role:    "leader",
			version: "v1.7.0",
			expectedManifests: []string{
				"https://github.com/antrea-io/antrea/releases/download/v1.7.0/antrea-multicluster-leader-global.yml",
				"https://github.com/antrea-io/antrea/releases/download/v1.7.0/antrea-multicluster-leader-namespaced.yml",
			},
		},
		{
			name:    "generate versioned member manifests",
			role:    "member",
			version: "v1.7.0",
			expectedManifests: []string{
				"https://github.com/antrea-io/antrea/releases/download/v1.7.0/antrea-multicluster-member.yml",
				"https://github.com/antrea-io/antrea/releases/download/v1.7.0/antrea-multicluster-member-global.yml",
			},
		},
		{
			name:        "invalid role",
			role:        "member1",
			version:     "latest",
			expectedErr: "invalid role: member1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualManifests, err := generateManifests(tt.role, tt.version)
			if err != nil {
				assert.Equal(t, tt.expectedErr, err.Error())
			} else if !reflect.DeepEqual(actualManifests, tt.expectedManifests) {
				t.Errorf("Expected %v but got %v", tt.expectedManifests, actualManifests)
			}
		})
	}
}

func TestCreateResources(t *testing.T) {
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(mcscheme.Scheme)
	fakeClient := fake.NewSimpleClientset()
	apiGroupResources, _ := restmapper.GetAPIGroupResources(fakeClient.Discovery())
	cmd := &cobra.Command{}
	file := filepath.Join("..", "..", "..", "..", "..", "multicluster", "build", "yamls", "antrea-multicluster-leader-global.yml")
	content, err := os.ReadFile(file)
	if err != nil {
		t.Errorf("Failed to open the file %s", file)
	}
	err = createResources(cmd, apiGroupResources, fakeDynamicClient, content)
	if err != nil {
		assert.Contains(t, err.Error(), "no matches for kind \"CustomResourceDefinition\"")
	}
}

func TestDeploy(t *testing.T) {
	fakeConfigs := []byte(`apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURJVENDQWdtZ0F3SUJBZ0lJTHJac3Z6ZFQ3ekF3RFFZSktvWklodmNOQVFFTEJRQXdGVEVUTUJFR0ExVUUKQXhNS2EzVmlaWEp1WlhSbGN6QWVGdzB5TWpBNE1qSXdNakl6TXpkYUZ3MHlNekE0TWpJd01qSXpNemxhTURReApGekFWQmdOVkJBb1REbk41YzNSbGJUcHRZWE4wWlhKek1Sa3dGd1lEVlFRREV4QnJkV0psY201bGRHVnpMV0ZrCmJXbHVNSUlCSWpBTkJna3Foa2lHOXcwQkFRRUZBQU9DQVE4QU1JSUJDZ0tDQVFFQTB4N2JEd2NqSzN3VjRGSzkKYUtrd0FUdjVoT2NsbHhUSEI1ejFUbHZJV3pmdTNYNjZtaWkxUE04ODI1dTArdDRRdisxUVRIRHFzUkNvWFA1awpuNGNWZkxkeTlad25uN01uSDExVTRsRWRoeXBrdlZsc0RmajlBdWh3WHBZVE82eE5kM2o2Y3BIZGNMOW9PbGw2CkowcGU2RzBleHpTSHMvbHRUZXlyalRGbXM2Sm5zSWV6T2lHRmhZOTJCbDBmZ1krb2p6MFEwM2cvcE5QZUszcGMKK05wTWh4eG1UY1lVNzlaZVRqV1JPYTFQSituNk1SMEhDbW0xQk5QNmdwWmozbGtWSktkZnBEYmovWHYvQWNkVQpab3E5Ym95aGNDUCtiYmgyaWVtaTc0bnZqZ1BUTkVDZWU2a3ZHY3VNaXRKUkdvWjBxbFpZbXZDaWdEeGlSTnBNClBPa1dud0lEQVFBQm8xWXdWREFPQmdOVkhROEJBZjhFQkFNQ0JhQXdFd1lEVlIwbEJBd3dDZ1lJS3dZQkJRVUgKQXdJd0RBWURWUjBUQVFIL0JBSXdBREFmQmdOVkhTTUVHREFXZ0JSc2VoZXVkM0l5VWRNdkhhRS9YU3MrOFErLwpiVEFOQmdrcWhraUc5dzBCQVFzRkFBT0NBUUVBcmg4UFRadFgvWjlHVzlMYmxZZ1FWWE04VlRrWEtGSEpTZldOCkJLNXo2NWNWdGN2cFZ0WDZNTlppTFhuYkFzQ0JPY1RqejBJRlphYkNNUkZzYmdYbEVqV0ZuRE5abzBMVHFTZUcKQ2RqTWljK0JzbmFGUThZOXJ5TTVxZ0RhQzNWQkdTSXVscklXeGxPYmRmUEpWRnpUaVNTcmJBR1Z3Uk5sQlpmYgpYOXBlRlpNNmNFNUhTOE5RTmNoZkh2SWhGSUVuR2YxOUx2enp0WGUzQWwwb3hYNjdRKzhyWXd0Tm56dS9xM29BCmJIN1dsNld5ODVYNS90RWlQcWU0ZU1GalRDME9tR2NHZ2lQdU90NjlIejAwV2hvaWNYYWpma1FZOHNKMk5Uc1cKdUcxbWZqb0tTdUN0OC9BRmhPNURlaHZ3eFNIQU12eG1VQUJYL294bU1DNzdwV0VnRWc9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==
    server: https://localhost
  name: fake-cluster
contexts:
- context:
    cluster:  fake-cluster
    user:  user-id
  name:  fake-cluster
current-context:  fake-cluster
kind: Config`)

	var err error
	fakeKubeconfig, err := os.CreateTemp("", "fakeKubeconfig")
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(fakeKubeconfig.Name())
	fakeKubeconfig.Write(fakeConfigs)
	kubeconfig := ""
	cmd := &cobra.Command{}
	cmd.Flags().StringVarP(&kubeconfig, "kubeconfig", "k", fakeKubeconfig.Name(), "path of kubeconfig")
	fakeClient := fake.NewSimpleClientset()

	tests := []struct {
		name        string
		body        string
		err         error
		expectedErr string
	}{
		{
			name:        "error when getting manifests",
			err:         errors.New("wrong link"),
			expectedErr: "wrong link",
		}, {
			name:        "error with wrong manifests",
			body:        "fakebody",
			expectedErr: "json: cannot unmarshal string into Go value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpGet = func(url string) (resp *http.Response, err error) {
				return &http.Response{Body: io.NopCloser(strings.NewReader(tt.body))}, tt.err
			}
			getAPIGroupResources = func(k8sClient kubernetes.Interface) ([]*restmapper.APIGroupResources, error) {
				return restmapper.GetAPIGroupResources(fakeClient.Discovery())
			}
			defer func() {
				httpGet = http.Get
				getAPIGroupResources = getAPIGroupResourcesWrapper
			}()
			gotErr := deploy(cmd, "leader", "latest", "kube-system", "")
			if tt.expectedErr != "" {
				if gotErr != nil {
					assert.Contains(t, gotErr.Error(), tt.expectedErr)
				} else {
					t.Error("Expected to get error but got nil")
				}
			}
		})
	}
}
