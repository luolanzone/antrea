// Copyright 2022 Antrea Authors
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
//

// Code generated by MockGen. DO NOT EDIT.
// Source: antrea.io/antrea/pkg/agent/secondarynetwork/podwatch (interfaces: InterfaceConfigurator)

// Package testing is a generated GoMock package.
package testing

import (
	types100 "github.com/containernetworking/cni/pkg/types/100"
	gomock "github.com/golang/mock/gomock"
	reflect "reflect"
)

// MockInterfaceConfigurator is a mock of InterfaceConfigurator interface
type MockInterfaceConfigurator struct {
	ctrl     *gomock.Controller
	recorder *MockInterfaceConfiguratorMockRecorder
}

// MockInterfaceConfiguratorMockRecorder is the mock recorder for MockInterfaceConfigurator
type MockInterfaceConfiguratorMockRecorder struct {
	mock *MockInterfaceConfigurator
}

// NewMockInterfaceConfigurator creates a new mock instance
func NewMockInterfaceConfigurator(ctrl *gomock.Controller) *MockInterfaceConfigurator {
	mock := &MockInterfaceConfigurator{ctrl: ctrl}
	mock.recorder = &MockInterfaceConfiguratorMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockInterfaceConfigurator) EXPECT() *MockInterfaceConfiguratorMockRecorder {
	return m.recorder
}

// ConfigureSriovSecondaryInterface mocks base method
func (m *MockInterfaceConfigurator) ConfigureSriovSecondaryInterface(arg0, arg1, arg2, arg3, arg4 string, arg5 int, arg6 string, arg7 *types100.Result) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ConfigureSriovSecondaryInterface", arg0, arg1, arg2, arg3, arg4, arg5, arg6, arg7)
	ret0, _ := ret[0].(error)
	return ret0
}

// ConfigureSriovSecondaryInterface indicates an expected call of ConfigureSriovSecondaryInterface
func (mr *MockInterfaceConfiguratorMockRecorder) ConfigureSriovSecondaryInterface(arg0, arg1, arg2, arg3, arg4, arg5, arg6, arg7 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ConfigureSriovSecondaryInterface", reflect.TypeOf((*MockInterfaceConfigurator)(nil).ConfigureSriovSecondaryInterface), arg0, arg1, arg2, arg3, arg4, arg5, arg6, arg7)
}