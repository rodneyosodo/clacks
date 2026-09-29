BINARY := clacks
CMD_PKG ?= ./cmd/clacks
MODULE := github.com/rodneyosodo/clacks
BUILD_DIR ?= bin
DIST_DIR ?= dist

SHELL := /bin/bash

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
GOARM ?= $(shell go env GOARM)
CGO_ENABLED ?= 0

VERSION ?= $(shell git describe --abbrev=0 --tags 2>/dev/null || echo 'v0.0.0')
COMMIT ?= $(shell git rev-parse HEAD)
COMMIT_TIME ?= $(shell git log -1 --date=format:"%Y-%m-%dT%H:%M:%S%z" --format=%cd)
TIME ?= $(shell date +'%Y-%m-%dT%H:%M:%SZ')

DOCKER_IMAGE_NAME_PREFIX ?= ghcr.io/rodneyosodo
DOCKER_IMAGE ?= $(DOCKER_IMAGE_NAME_PREFIX)/$(BINARY)
DOCKER_PLATFORMS ?= linux/amd64,linux/arm64
TAGS ?= $(DOCKER_IMAGE):latest
comma := ,
# --tag takes one value per flag, so a comma-separated list expands to repeated flags.
TAG_ARGS = $(foreach t,$(subst $(comma), ,$(TAGS)),--tag $(t))

LDFLAGS := -s -w \
	-X 'main.version=$(VERSION)' \
	-X 'main.commit=$(COMMIT)' \
	-X 'main.commitTime=$(COMMIT_TIME)' \
	-X 'main.buildTime=$(TIME)'

.PHONY: build build-linux print-meta package checksums test race vet lint fmt check \
	clean docker docker-buildx docker-dev docker-push compose-up compose-down compose-logs

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=$(GOARM) \
		go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD_PKG)

print-meta:
	@echo "version=$(VERSION)"
	@echo "commit=$(COMMIT)"
	@echo "commit_time=$(COMMIT_TIME)"
	@echo "time=$(TIME)"
	@echo "image=$(DOCKER_IMAGE)"

package:
	@set -euo pipefail; \
	ext=""; \
	if [ "$(GOOS)" = "windows" ]; then ext=".exe"; fi; \
	base="$(BINARY)-$(GOOS)-$(GOARCH)"; \
	name="$$base$$ext"; \
	mkdir -p "$(DIST_DIR)"; \
	distabs="$$(cd "$(DIST_DIR)" && pwd)"; \
	stage="$$distabs/.stage-$(GOOS)-$(GOARCH)"; \
	rm -rf "$$stage"; \
	mkdir -p "$$stage"; \
	$(MAKE) --no-print-directory build GOOS=$(GOOS) GOARCH=$(GOARCH) VERSION=$(VERSION); \
	cp "$(BUILD_DIR)/$(BINARY)" "$$stage/$$name"; \
	cp LICENSE README.md "$$stage/"; \
	if [ "$(GOOS)" = "windows" ]; then \
		(cd "$$stage" && zip -q "$$distabs/$$base.zip" "$$name" LICENSE README.md); \
	else \
		tar -czf "$$distabs/$$base.tar.gz" -C "$$stage" "$$name" LICENSE README.md; \
	fi; \
	rm -rf "$$stage"; \
	echo "packaged $(DIST_DIR)/$$base"

checksums:
	@set -euo pipefail; \
	mkdir -p "$(DIST_DIR)"; \
	cd "$(DIST_DIR)"; \
	{ ls -1 *.tar.gz *.zip 2>/dev/null || true; } | sort \
		| xargs -r sha256sum > checksums.txt; \
	cat checksums.txt

# Static binary for any machine of the same arch.
build-linux:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux $(CMD_PKG)

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

check: fmt vet lint test

clean:
	rm -rf $(BUILD_DIR)

# Full multi-stage image: builds from source inside the container.
# Tagged with the release version and latest.
docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg COMMIT_TIME=$(COMMIT_TIME) \
		--build-arg TIME=$(TIME) \
		--tag=$(DOCKER_IMAGE):$(VERSION) \
		--tag=$(DOCKER_IMAGE):latest \
		-f docker/Dockerfile .

# Fast image: bakes in a binary you already built with `make build`.
docker-dev:
	docker build \
		--build-arg BINARY=$(BUILD_DIR)/$(BINARY) \
		--tag=$(DOCKER_IMAGE):latest \
		-f docker/Dockerfile.dev .

docker-buildx:
	docker buildx build \
		--platform $(DOCKER_PLATFORMS) \
		--file docker/Dockerfile \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg COMMIT_TIME=$(COMMIT_TIME) \
		--build-arg TIME=$(TIME) \
		$(TAG_ARGS) \
		$(if $(filter true,$(DOCKER_PUSH)),--push,) \
		--cache-from type=gha \
		--cache-to type=gha,mode=max \
		.

docker-push: docker
	@echo "pushing $(DOCKER_IMAGE):$(VERSION) $(DOCKER_IMAGE):latest"
	@docker push $(DOCKER_IMAGE):$(VERSION)
	@docker push $(DOCKER_IMAGE):latest

compose-up:
	docker compose -f docker/compose.yaml up -d

compose-down:
	docker compose -f docker/compose.yaml down

compose-logs:
	docker compose -f docker/compose.yaml logs -f
