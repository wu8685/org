.PHONY: check-tools kind-up kind-down temporal-dev console-dev console-test docs-test backend-test sdk-temporal-test sample-test sample-image sample-kind-load parallel-sample-test parallel-sample-image parallel-sample-kind-load dynamic-sample-test dynamic-sample-image dynamic-sample-kind-load e2e-preflight e2e-local parallel-e2e-local dynamic-e2e-local e2e-clean

SAMPLE_IMAGE_REPOSITORY ?=
SAMPLE_VERSION ?=
SAMPLE_COMMIT ?=

check-tools:
	@command -v docker >/dev/null
	@command -v kubectl >/dev/null
	@command -v kind >/dev/null
	@command -v temporal >/dev/null

kind-up:
	@kind get clusters | grep -qx org || kind create cluster --name org --config deploy/dev/kind.yaml

kind-down:
	@kind delete cluster --name org

temporal-dev:
	@mkdir -p .org
	@temporal server start-dev --port 7233 --ui-port 8080 --db-filename .org/temporal.db

console-dev:
	@go run ./cmd/org-console

console-test:
	@go test ./internal/console ./cmd/org-console

docs-test:
	@sh scripts/check-doc-terminology.sh

backend-test:
	@go test ./...

sdk-temporal-test:
	@ORG_SDK_TEMPORAL_TEST=1 go test ./test/sdk_runtime -run '^TestOrgSDKWaitSurvivesWorkerRestartOnLocalTemporal$$' -count=1 -v

sample-test:
	@$(MAKE) -C samples/hello test

sample-image:
	@$(MAKE) -C samples/hello image VERSION="$(SAMPLE_VERSION)" COMMIT="$(SAMPLE_COMMIT)" $(if $(SAMPLE_IMAGE_REPOSITORY),IMAGE_REPOSITORY="$(SAMPLE_IMAGE_REPOSITORY)")

sample-kind-load:
	@$(MAKE) -C samples/hello kind-load VERSION="$(SAMPLE_VERSION)" COMMIT="$(SAMPLE_COMMIT)" $(if $(SAMPLE_IMAGE_REPOSITORY),IMAGE_REPOSITORY="$(SAMPLE_IMAGE_REPOSITORY)")

parallel-sample-test:
	@$(MAKE) -C samples/parallel-confirmation test

parallel-sample-image:
	@$(MAKE) -C samples/parallel-confirmation image VERSION="$(SAMPLE_VERSION)" COMMIT="$(SAMPLE_COMMIT)" $(if $(SAMPLE_IMAGE_REPOSITORY),IMAGE_REPOSITORY="$(SAMPLE_IMAGE_REPOSITORY)")

parallel-sample-kind-load:
	@$(MAKE) -C samples/parallel-confirmation kind-load VERSION="$(SAMPLE_VERSION)" COMMIT="$(SAMPLE_COMMIT)" $(if $(SAMPLE_IMAGE_REPOSITORY),IMAGE_REPOSITORY="$(SAMPLE_IMAGE_REPOSITORY)")

dynamic-sample-test:
	@$(MAKE) -C samples/dynamic-decision test

dynamic-sample-image:
	@$(MAKE) -C samples/dynamic-decision image VERSION="$(SAMPLE_VERSION)" COMMIT="$(SAMPLE_COMMIT)" $(if $(SAMPLE_IMAGE_REPOSITORY),IMAGE_REPOSITORY="$(SAMPLE_IMAGE_REPOSITORY)")

dynamic-sample-kind-load:
	@$(MAKE) -C samples/dynamic-decision kind-load VERSION="$(SAMPLE_VERSION)" COMMIT="$(SAMPLE_COMMIT)" $(if $(SAMPLE_IMAGE_REPOSITORY),IMAGE_REPOSITORY="$(SAMPLE_IMAGE_REPOSITORY)")

e2e-preflight:
	@sh scripts/e2e-preflight.sh

e2e-local: e2e-preflight
	@ORG_E2E=1 go test ./test/e2e -run '^TestLocalControlPlaneAcceptance$$' -count=1 -v

parallel-e2e-local: e2e-preflight
	@ORG_E2E=1 go test ./test/e2e -run '^TestLocalParallelConfirmationAcceptance$$' -count=1 -v

dynamic-e2e-local: e2e-preflight
	@ORG_E2E=1 go test ./test/e2e -run '^TestLocalDynamicDecisionAcceptance$$' -count=1 -v

e2e-clean:
	@test -n "$(RUN_ID)" || { echo "RUN_ID is required: make e2e-clean RUN_ID=<printed-id>" >&2; exit 2; }
	@RUN_ID="$(RUN_ID)" sh scripts/e2e-clean.sh
