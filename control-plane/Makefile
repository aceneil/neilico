.PHONY: fmt vet test build run

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./... -count=1

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -o bin/neilico-api ./cmd/api

run:
	go run ./cmd/api -config configs/config.example.yaml
