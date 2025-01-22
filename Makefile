# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
BINARY_NAME=cc-intel-platform-registration

# Tools
GOLANGCI_LINT=golangci-lint
STATICCHECK=staticcheck
GOVULNCHECK=$(GOCMD) run golang.org/x/vuln/cmd/govulncheck@latest

.PHONY: all build test clean deps lint security-check check

all: check build

build:
	$(GOBUILD) -o $(BINARY_NAME) -v

test:
	$(GOTEST) -v ./...

clean:
	$(GOCMD) clean
	rm -f $(BINARY_NAME)

deps:
	$(GOMOD) download
	$(GOMOD) tidy
	$(GOGET) -u honnef.co/go/tools/cmd/staticcheck
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.63.4

lint:
	$(GOLANGCI_LINT) run
	$(STATICCHECK) ./...
	$(GOCMD) vet ./...

deadcode:
	@echo "Checking for dead code..."
	$(GOCMD) run golang.org/x/tools/cmd/deadcode@latest ./...

security-check:
	$(GOVULNCHECK) ./...

# Combined check target that runs all verifications
check: deps lint deadcode security-check test