.PHONY: fmt fmt-check test race vet build demo test-update check

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

# test-update: an ordinary build always reports version "dev" (only
# cmd/release's real release build sets it via ldflags), and
# update.IsNewer treats "dev" as never eligible for an update, by design.
# -fake-version pretends to be an old real version instead, so the
# update-check flow (Settings > Check for update, and the automatic
# on-launch check) has something to actually offer an update over.
test-update:
	go run -buildvcs=false ./cmd/whatthedock --demo -fake-version=v0.1.0

check: fmt-check test race vet build
