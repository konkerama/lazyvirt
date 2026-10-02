BIN     := lazyvirt
WORKERS ?= 2

.DEFAULT_GOAL := help
.PHONY: help build run test test-integration bench vet install vms-up vms-down

help: ## List targets
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sort | \
		awk -F':.*##' '{printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2}'

build: ## Build ./lazyvirt
	go build -trimpath -ldflags='-s -w' -o $(BIN) .

run: build ## Run against local libvirt (ARGS='-c qemu+ssh://host/system --prefix talos-local')
	./$(BIN) $(ARGS)

test: vet ## Unit tests (no libvirt needed)
	go test ./...

test-integration: ## Tests against real libvirt; needs `make vms-up`
	go test -tags integration -count=1 -v -run Integration ./...

bench: ## Per-tick parse cost
	go test -run '^$$' -bench . -benchmem ./...

vet: ## go vet
	go vet ./...

install: ## go install into $GOBIN / ~/go/bin
	go install -trimpath -ldflags='-s -w' .

vms-up: ## Create lazyvirt-test-* cirros VMs (WORKERS=2)
	scripts/test-vms.sh up $(WORKERS)

vms-down: ## Remove all lazyvirt-test-* VMs, pool and network
	scripts/test-vms.sh down
