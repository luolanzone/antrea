#!/bin/bash
set -e
DOCKER_REGISTRY="$(head -n1 ci/docker-registry)"
if [[ ! -d .kube ]];then
  mkdir .kube
fi
#./ci/jenkins/test-mc.sh --testcase e2e --registry ${DOCKER_REGISTRY} --mc-gateway --codecov-token "${CODECOV_TOKEN}" --coverage --kind
./ci/jenkins/test-mc.sh --testcase e2e --mc-gateway --kind --debug
