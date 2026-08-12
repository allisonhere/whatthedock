.PHONY: fmt test vet build demo check

fmt:
	gofmt -w .

test:
	go test ./...

vet:
	go vet ./...

build:
	go build -buildvcs=false ./...

demo:
	go run -buildvcs=false ./cmd/whatthedock --demo

check: fmt test vet build
