all: build

TAG?=dev
FLAGS=
LDFLAGS?=-s
ENVVAR=CGO_ENABLED=0 GO111MODULE=off
GOOS?=linux
REGISTRY?=staging-k8s.gcr.io
ifdef BUILD_TAGS
  TAGS_FLAG=--tags ${BUILD_TAGS}
  PROVIDER=-${BUILD_TAGS}
  FOR_PROVIDER=" for ${BUILD_TAGS}"
else
  TAGS_FLAG=
  PROVIDER=
  FOR_PROVIDER=
endif
ifdef LDFLAGS
  LDFLAGS_FLAG=--ldflags "${LDFLAGS}"
else
  LDFLAGS_FLAG=
endif

# deps:
# 	go mod vendor
# 	cp ../cluster-autoscaler/cloudprovider/grpc/grpc.proto grpc/grpc.proto
# 	go get -v google.golang.org/grpc@v1.26.0
# 	go get -v github.com/golang/protobuf@v1.3.2
# 	go get -v github.com/golang/protobuf/protoc-gen-go@v1.3.2
# 	protoc -I . -I vendor grpc/grpc.proto --go_out=plugins=grpc:.

build: clean deps
	$(ENVVAR) GOOS=$(GOOS) go build ${LDFLAGS_FLAG} ${TAGS_FLAG} ./...
	$(ENVVAR) GOOS=$(GOOS) go build -o vsphere-autoscaler ${LDFLAGS_FLAG} ${TAGS_FLAG}

build-binary: clean deps
	$(ENVVAR) GOOS=$(GOOS) go build -o vsphere-autoscaler ${LDFLAGS_FLAG} ${TAGS_FLAG}

test-unit: clean deps build
	GO111MODULE=off go test --test.short -race ./... ${TAGS_FLAG}

dev-release: build-binary execute-release
	@echo "Release ${TAG}${FOR_PROVIDER} completed"

make-image:
ifdef BASEIMAGE
	docker build --pull --build-arg BASEIMAGE=${BASEIMAGE} \
	    -t ${REGISTRY}/vsphere-autoscaler${PROVIDER}:${TAG} .
else
	docker build --pull -t ${REGISTRY}/vsphere-autoscaler${PROVIDER}:${TAG} .
endif

push-image:
	./push_image.sh ${REGISTRY}/vsphere-autoscaler${PROVIDER}:${TAG}

execute-release: make-image push-image

clean:
	rm -f vsphere-autoscaler

format:
	test -z "$$(find . -path ./vendor -prune -type f -o -name '*.go' -exec gofmt -s -d {} + | tee /dev/stderr)" || \
    test -z "$$(find . -path ./vendor -prune -type f -o -name '*.go' -exec gofmt -s -w {} + | tee /dev/stderr)"

docker-builder:
	docker build -t vsphere-autoscaling-builder ./builder

build-in-docker: clean docker-builder
	docker run --rm -v `pwd`:/gopath/src/github.com/nmarus/autoscaler/vsphere-autoscaler/ vsphere-autoscaling-builder:latest bash -c 'cd /gopath/src/github.com/nmarus/autoscaler/vsphere-autoscaler && BUILD_TAGS=${BUILD_TAGS} LDFLAGS="${LDFLAGS}" make build-binary'

release: build-in-docker execute-release
	@echo "Full in-docker release ${TAG}${FOR_PROVIDER} completed"

container: build-in-docker make-image
	@echo "Created in-docker image ${TAG}${FOR_PROVIDER}"

test-in-docker: clean docker-builder
	docker run --rm -v `pwd`:/gopath/src/github.com/nmarus/autoscaler/vsphere-autoscaler/ vsphere-autoscaling-builder:latest bash -c 'cd /gopath/src/github.com/nmarus/autoscaler/vsphere-autoscaler && GO111MODULE=off go test -race ./... ${TAGS_FLAG}'

.PHONY: all deps build test-unit clean format execute-release dev-release docker-builder build-in-docker release
