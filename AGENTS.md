# TideDock Agent Notes

- Work in this repository only: `/home/allieb/Projects/tidedock`.
- Do not edit sibling Tide repositories such as `../tideui` or `../tidemail` unless the user explicitly asks for cross-repo work.
- TideDock remote is `https://github.com/allisonhere/tidedock.git` on branch `main`.
- The user originally provided `git@github.com:allisonhere/tidedock.git`, but SSH failed locally because of system SSH config permissions. HTTPS is the working remote.
- TideDock depends on TideUI through the published Go module, not a local `replace`.
- Current TideUI dependency is pinned in `go.mod`; keep it resolvable for GitHub Actions/CI.
- Before claiming code work is done, run the normal checks from this repo:
  - `go test ./...`
  - `go test -race ./...`
  - `go vet ./...`
  - `go build -buildvcs=false ./...`
  - `gofmt -l .`
