.PHONY: fmt fmt-check test race vet build demo test-update check icons

# icons regenerates the embedded app icons from assets/icon-source.png: the
# white corners become transparent, then 512px and 256px copies are made.
icons:
	magick assets/icon-source.png -alpha set -fuzz 6% -fill none \
		-draw "color 0,0 floodfill" -draw "color 1253,0 floodfill" \
		-draw "color 0,1253 floodfill" -draw "color 1253,1253 floodfill" \
		-channel A -blur 0x1 -level 50%,100% +channel -trim +repage \
		-resize 512x512 -background none -gravity center -extent 512x512 -strip assets/icon-512.png
	magick assets/icon-512.png -resize 256x256 -strip assets/icon-256.png

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
