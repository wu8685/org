.PHONY: test vet verify image push kind-load require-build-inputs

IMAGE_REPOSITORY ?= org.local/hello-worker
VERSION ?=
COMMIT ?=
KIND_CLUSTER ?= org

test:
	@GOWORK=off go test ./...

vet:
	@GOWORK=off go vet ./...

verify: test vet

require-build-inputs:
	@test -n "$(VERSION)" || { echo "VERSION is required" >&2; exit 2; }
	@test -n "$(COMMIT)" || { echo "COMMIT is required" >&2; exit 2; }

image: require-build-inputs
	@IMAGE_REPOSITORY="$(IMAGE_REPOSITORY)" sh scripts/build-image.sh "$(VERSION)" "$(COMMIT)"

push: require-build-inputs
	@IMAGE_REPOSITORY="$(IMAGE_REPOSITORY)" sh scripts/build-image.sh "$(VERSION)" "$(COMMIT)" --push

kind-load: require-build-inputs
	@IMAGE_REPOSITORY="$(IMAGE_REPOSITORY)" KIND_CLUSTER="$(KIND_CLUSTER)" sh scripts/build-image.sh "$(VERSION)" "$(COMMIT)" --kind-load
