.PHONY: build test vet install
build:
	go build -o bin/paperless$(shell go env GOEXE) .
test:
	go test ./...
vet:
	go vet ./...
install:
ifeq ($(OS),Windows_NT)
	powershell -NoProfile -ExecutionPolicy Bypass -File ./install.ps1
else
	go build -o "$(shell go env GOPATH)/bin/paperless$(shell go env GOEXE)" .
	@echo 'Ensure your Go bin directory is on PATH: export PATH="$$(go env GOPATH)/bin:$$PATH"'
endif
