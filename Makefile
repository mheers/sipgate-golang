SHELL := /bin/sh

MODULE    := github.com/mheers/sipgate-golang
BIN       := sipgate
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X $(MODULE).Version=$(VERSION)

SPEC      := spec/sipgate-v2.json
OPENAPI   := npx -y @openapitools/openapi-generator-cli generate
OPENAPI_PROPERTIES := packageName=openapi,isGoSubmodule=true,generateInterfaces=true,enumClassPrefix=true

.PHONY: all build install test race cover vet fmt lint spec generate generate-from-upstream e2e clean

all: fmt vet test

build:
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/sipgate

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/sipgate

test:
	go test ./...

race:
	go test -race ./...

# Read-only smoke test against the live sipgate API. Credentials are read from
# the untracked .env.dev file.
e2e:
	@test -f .env.dev || (echo "missing .env.dev with SIPGATE_TOKEN_ID/SIPGATE_TOKEN or SIPGATE_ACCESS_TOKEN"; exit 1)
	@set -a; . ./.env.dev; set +a; go test -tags=integration ./internal/cli -run TestLiveReadOnly -v

cover:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -n 1

vet:
	go vet ./...

fmt:
	@find . -name '*.go' -not -path './openapi/*' -print0 | xargs -0 gofmt -w

lint: vet
	@test -z "$$(gofmt -l . | grep -v '^openapi/' || true)" || (echo "gofmt required for:"; gofmt -l . | grep -v '^openapi/'; exit 1)

# Download and normalize the upstream Swagger document. The result is checked
# in so that generation is reproducible without network access to sipgate.
spec:
	go run ./tools/specgen -out $(SPEC)

# Refresh the spec from the live API and regenerate the client.
generate-from-upstream: spec generate

# Regenerate the typed OpenAPI client from the checked-in spec. Requires Java
# 11+ and network access for npx. The generator version is pinned in
# openapitools.json.
generate:
	@test -f $(SPEC) || (echo "missing $(SPEC); run make spec first"; exit 1)
	$(OPENAPI) -i $(SPEC) -g go -o openapi \
		--global-property apis,models,supportingFiles,apiTests=false,modelTests=false,apiDocs=false,modelDocs=false \
		-p "$(OPENAPI_PROPERTIES)"
	rm -f openapi/go.mod openapi/go.sum openapi/README.md openapi/.travis.yml \
		openapi/git_push.sh openapi/.gitignore openapi/.openapi-generator-ignore \
		openapi/.openapi-generator/FILES
	go run ./tools/genfix

clean:
	rm -rf bin dist coverage.out
