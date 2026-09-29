BINARY := clacks
MODULE := github.com/rodneyosodo/clacks
BUILD_DIR ?= bin

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
GOARM ?= $(shell go env GOARM)
CGO_ENABLED ?= 0

VERSION ?= $(shell git describe --abbrev=0 --tags 2>/dev/null || echo 'v0.0.0')
COMMIT ?= $(shell git rev-parse HEAD)
COMMIT_TIME ?= $(shell git log -1 --date=format:"%Y-%m-%dT%H:%M:%S%z" --format=%cd)
TIME ?= $(shell date +'%Y-%m-%dT%H:%M:%S%z')

DOCKER_IMAGE_NAME_PREFIX ?= ghcr.io/rodneyosodo
DOCKER_IMAGE ?= $(DOCKER_IMAGE_NAME_PREFIX)/$(BINARY)

LDFLAGS := -s -w \
	-X 'main.version=$(VERSION)' \
	-X 'main.commit=$(COMMIT)' \
	-X 'main.commitTime=$(COMMIT_TIME)' \
	-X 'main.buildTime=$(TIME)'

.PHONY: build build-linux test race vet lint fmt check clean docker docker-dev docker-push compose-up compose-down compose-logs

build:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=$(GOARM) \
		go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/$(BINARY)

# Static binary for any machine of the same arch.
build-linux:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux ./cmd/$(BINARY)

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
