FROM antrea/base-ubuntu:2.15.1

LABEL maintainer="Antrea <projectantrea-dev@googlegroups.com>"
LABEL description="The Docker image to deploy the Antrea CNI."

USER root

COPY build/images/scripts/* /usr/local/bin/
COPY bin/* /usr/local/bin/
