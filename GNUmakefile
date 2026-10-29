GOLANGCI_LINT_VERSION ?= v2.14.0
HC_INSTALL_VERSION ?= v0.9.5

default: build

# CI runs deps, build, lint and test.

# Terraform is installed only when it is missing, because the provider tests,
# the docs generator and terraform fmt all need it on PATH.
deps:
	go mod download
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@command -v terraform > /dev/null || \
		go run github.com/hashicorp/hc-install/cmd/hc-install@$(HC_INSTALL_VERSION) install -path "$$(go env GOPATH)/bin" terraform

build:
	go build -v ./...

install:
	go install -v ./...

test:
	go test -cover -timeout=15m ./...

lint: docs-check
	golangci-lint run ./...
	terraform fmt -recursive -check examples tests

fmt:
	gofmt -s -w -e .
	terraform fmt -recursive examples tests

docs:
	go generate ./...

# Fails when docs/ is out of date with the schema, templates or examples.
docs-check: docs
	@git diff --exit-code -- docs templates || \
		(echo "docs/ is out of date. Run make docs and commit the result." && exit 1)
	@test -z "$$(git ls-files --others --exclude-standard -- docs templates)" || \
		(echo "make docs created files that are not committed. Commit them." && exit 1)

# Runs the Terragrunt suite against a real Plerion API. See tests/terragrunt/README.md.
e2e:
	tests/terragrunt/run.sh

.PHONY: default deps build install test lint fmt docs docs-check e2e
