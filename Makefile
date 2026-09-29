BINARY := clacks
MODULE := github.com/rodneyosodo/clacks

.PHONY: build test vet lint clean

build:
	go build -o bin/$(BINARY) ./cmd/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin
