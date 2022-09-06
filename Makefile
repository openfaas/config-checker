IMG_NAME?=openfaas-checker

TAG?=latest
OWNER?=alexellis2
SERVER?=docker.io

export DOCKER_CLI_EXPERIMENTAL=enabled
export DOCKER_BUILDKIT=1

PLATFORM?=linux/arm/v7,linux/arm64,linux/amd64

.PHONY: test
test:
	CGO_ENABLED=0 go test $(shell go list ./... | grep -v /vendor/|xargs echo) -cover

.PHONY: publish
publish:
	@echo  $(SERVER)/$(OWNER)/$(IMG_NAME):$(TAG) && \
	docker buildx create --use --name=multiarch --node=multiarch && \
	docker buildx build \
		--platform $(PLATFORM) \
		--push=true \
		--tag $(SERVER)/$(OWNER)/$(IMG_NAME):$(TAG) \
		.
