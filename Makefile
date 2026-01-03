MODULE := github.com/alcounit/browser-controller
APIS_PKG := apis
GROUP := selenosis
BROWSER := browser
VERSION := v1
BOILERPLATE := hack/boilerplate.go.txt

PROJECT_PKG := $(MODULE)/pkg
LISTER_PKG := $(MODULE)/pkg/listers
INFORMER_PKG := $(MODULE)/pkg/informers
API_DIR := $(APIS_PKG)/$(GROUP)/$(VERSION)

CONTROLLER_GEN := $(shell which controller-gen)
CLIENT_GEN := $(shell which client-gen)
LISTER_GEN := $(shell which lister-gen)
INFORMER_GEN := $(shell which informer-gen)

BINARY_NAME := manager
DOCKER_REGISTRY ?= ${REGISTRY}
IMAGE_NAME := $(DOCKER_REGISTRY)/selenosis-controller
IMAGE_TAG ?= v1.0.1
IMG := $(IMAGE_NAME):$(IMAGE_TAG)
PLATFORM ?= linux/amd64

CONTAINER_TOOL ?= docker

.PHONY: all generate deepcopy client lister informer manifests install-tools verify clean fmt vet tidy docker-build docker-push deploy install help show-vars

all: generate manifests

install-tools:
	@go install k8s.io/code-generator/cmd/{deepcopy-gen,client-gen,lister-gen,informer-gen}@latest
	@go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest

generate: deepcopy client lister informer

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

deepcopy:
	@$(CONTROLLER_GEN) \
		object:headerFile=$(BOILERPLATE) \
		crd:crdVersions=v1 \
		rbac:roleName=manager-role \
		paths="./apis/..."

client:
	@$(CLIENT_GEN) \
		--clientset-name clientset \
		--input-base "$(MODULE)/apis" \
		--input $(BROWSER)/$(VERSION) \
		--output-pkg $(PROJECT_PKG) \
		--output-dir ./pkg \
		--go-header-file $(BOILERPLATE)

lister:
	@$(LISTER_GEN) \
  		--output-pkg $(LISTER_PKG) \
  		--output-dir ./pkg/listers \
  		--go-header-file $(BOILERPLATE) \
  		$(MODULE)/$(APIS_PKG)/$(BROWSER)/$(VERSION)

informer:
	@$(INFORMER_GEN) \
		--versioned-clientset-package $(PROJECT_PKG)/clientset \
		--listers-package $(LISTER_PKG) \
		--output-pkg $(INFORMER_PKG) \
		--output-dir ./pkg/informers \
		--go-header-file $(BOILERPLATE) \
		$(MODULE)/$(APIS_PKG)/$(BROWSER)/$(VERSION)

manifests:
	@$(CONTROLLER_GEN) \
		crd \
		rbac:roleName=browser-controller \
		paths="$(MODULE)/..." \
		output:crd:artifacts:config=config/crd \
		output:rbac:artifacts:config=config/rbac

verify:
	@git diff --exit-code || (echo "Generated code is out of date. Run 'make generate'." && exit 1)

clean:
	@rm -rf pkg/clientset pkg/listers pkg/informers
	@find $(APIS_PKG) -name 'zz_generated.deepcopy.go' -delete
	$(CONTAINER_TOOL) rmi $(IMG) 2>/dev/null || true

docker-build: manifests generate tidy fmt vet
	$(CONTAINER_TOOL) build --platform $(PLATFORM) -t $(IMG) .

docker-push:
	$(CONTAINER_TOOL) push $(IMG)

deploy: docker-build docker-push

show-vars:
	@echo "MODULE: $(MODULE)"
	@echo "BINARY_NAME: $(BINARY_NAME)"
	@echo "DOCKER_REGISTRY: $(DOCKER_REGISTRY)"
	@echo "IMAGE_NAME: $(IMAGE_NAME)"
	@echo "IMAGE_TAG: $(IMAGE_TAG)"
	@echo "IMG: $(IMG)"
	@echo "PLATFORM: $(PLATFORM)"
	@echo "CONTAINER_TOOL: $(CONTAINER_TOOL)"

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
