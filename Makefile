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

BINARY_NAME := browser-controller

REGISTRY ?= localhost:5000
IMAGE_NAME := $(REGISTRY)/$(BINARY_NAME)

VERSION ?= develop
EXTRA_TAGS ?=
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

docker-build: manifests generate tidy fmt vet
	$(CONTAINER_TOOL) buildx build \
		--platform $(PLATFORM) \
		-t $(IMAGE_NAME):$(VERSION) \
		--load \
		.

docker-push: manifests generate tidy fmt vet
	$(CONTAINER_TOOL) buildx build \
		--platform $(PLATFORM) \
		-t $(IMAGE_NAME):$(VERSION) \
		$(EXTRA_TAGS) \
		--push \
		.

deploy: docker-push

clean:
	$(CONTAINER_TOOL) rmi $(IMAGE_NAME):$(VERSION) 2>/dev/null || true

show-vars:
	@echo "BINARY_NAME: $(BINARY_NAME)"
	@echo "REGISTRY: $(REGISTRY)"
	@echo "IMAGE_NAME: $(IMAGE_NAME)"
	@echo "VERSION: $(VERSION)"
	@echo "PLATFORM: $(PLATFORM)"
	@echo "CONTAINER_TOOL: $(CONTAINER_TOOL)"

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
