.PHONY: build run tidy vet lint test docker-build

build:
	go build -o bin/scheduler ./cmd/scheduler

run:
	go run ./cmd/scheduler

tidy:
	go mod tidy

vet:
	go vet ./...

lint:
	golangci-lint run ./...

test:
	go test ./...

docker-build:
	docker build -t svc-scheduler .
