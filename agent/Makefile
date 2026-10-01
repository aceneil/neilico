.PHONY: build test vet fmt

build:
	CGO_ENABLED=0 go build ./...

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -w .
