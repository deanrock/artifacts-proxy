build:
	CGO_ENABLED=0 go build -ldflags="-s -w" ./cmd/artifacts-proxy
	CGO_ENABLED=0 go build -ldflags="-s -w" ./cmd/list-cached

test:
	go test -cover -timeout 120s ./...

test-e2e:
	go test -cover -timeout 180s -tags e2e ./...
