size file:
    @ls -la {{ file }} | awk '{printf "%.2fMb %s\n", $5/1024/1024, $NF}'

build-host:
    go tool templ generate
    go build -o=mcphost ./cmd/mcphost
    @just size ./mcphost

build-proxy:
    go build -o=mcpproxy ./cmd/mcpproxy
    @just size ./mcpproxy

build-testserver:
    go build -o=testserver ./cmd/testserver
    @just size ./testserver

build: build-host build-proxy build-testserver

host *args: build-host
    ./mcphost {{ args }}

proxy *args: build-proxy
    ./mcpproxy {{ args }}

demo: build-host build-testserver
    ./mcphost ./testserver

test:
    go test ./...

fd := `command -v fdfind || command -v fd`

watch cmd:
    {{ fd }} --extension go --extension templ --extension css --extension js --exclude '*_templ.go' | entr -ccr {{ cmd }}

install-templ:
    go get -tool github.com/a-h/templ/cmd/templ@latest

line-count:
    scc --exclude-file _templ.go,_test.go --no-cocomo --no-complexity --no-size
