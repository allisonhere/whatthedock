.PHONY: fmt fmt-check test race vet build demo check

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build -buildvcs=false ./...

demo:
	go run -buildvcs=false ./cmd/whatthedock --demo

check: fmt-check test race vet build
