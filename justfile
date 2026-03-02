# Helpers
_build cmd:
    go build -o=/tmp/bin/{{cmd}} ./cmd/{{cmd}}
    @stat -c "%s %n" /tmp/bin/{{cmd}} | awk '{printf "%.2fMb %s\n", $1/1024/1024, $2}'

_run cmd args="":
    @killall -9 {{cmd}} 2>/dev/null || true;
    @/tmp/bin/{{cmd}} {{args}}

_templ-generate:
    go tool templ generate

# Build targets
build-testserver: (_build "testserver")
build-proxy: (_build "mcpproxy")
build-host: build-testserver _templ-generate (_build "mcphost")
build: build-host build-proxy

# Run targets
run-host args="": build-host (_run "mcphost" args)
run-proxy args="": build-proxy (_run "mcpproxy" args)

test:
    go test ./...

watch cmd:
    fdfind --extension go --extension templ --extension css --extension js --exclude '*_templ.go' | entr -ccr {{ cmd }}

install-templ:
    go get -tool github.com/a-h/templ/cmd/templ@latest
