build:
	CGO_ENABLED=0 go build -ldflags="-s -w" ./cmd/artifacts-proxy
	CGO_ENABLED=0 go build -ldflags="-s -w" ./cmd/list-cached

test:
	go test -timeout 120s -v ./...

test-e2e:
	go test -timeout 120s -v -tags e2e ./...

buildx:
	
