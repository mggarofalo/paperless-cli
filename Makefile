.PHONY: build test vet install
build:
	go build -o bin/paperless$(shell go env GOEXE) .
test:
	go test ./...
vet:
	go vet ./...
install:
	go build -o "$(shell go env GOPATH)/bin/paperless$(shell go env GOEXE)" .
