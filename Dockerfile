# Copyright 2019 Frederic Boltz Author. All rights reserved
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

ARG BASEIMAGE=k8s.gcr.io/debian-base-amd64:v1.0.0
FROM $BASEIMAGE
LABEL maintainer="Frederic Boltz <frederic.boltz@gmail.com>"

ENV DEBIAN_FRONTEND noninteractive
RUN clean-install ca-certificates tzdata; \
    apt-get update; \
    apt-get install openssh-client curl -y; \
    cd /usr/local/bin; \
    KUBERNETES_VERSION=v1.18.8; \
    curl -L --remote-name-all https://storage.googleapis.com/kubernetes-release/release/${KUBERNETES_VERSION}/bin/linux/amd64/kubectl; chmod +x kubectl

COPY vsphere-autoscaler /
CMD ["/vsphere-autoscaler"]
