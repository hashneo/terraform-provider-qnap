default: build

HOSTNAME=registry.terraform.io
NAMESPACE=local/qnap
NAME=qnap
VERSION=0.1.0
OS_ARCH=$(shell go env GOOS)_$(shell go env GOARCH)

BINARY=terraform-provider-$(NAME)
INSTALL_PATH=~/.terraform.d/plugins/$(HOSTNAME)/$(NAMESPACE)/$(NAME)/$(VERSION)/$(OS_ARCH)

.PHONY: build
build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

.PHONY: install
install: build
	mkdir -p $(INSTALL_PATH)
	cp $(BINARY) $(INSTALL_PATH)/$(BINARY)
	@echo "Provider installed to $(INSTALL_PATH)"
	@echo "Add the following to your terraform required_providers block:"
	@echo ""
	@echo '  qnap = {'
	@echo '    source  = "$(HOSTNAME)/$(NAMESPACE)/$(NAME)"'
	@echo '    version = "$(VERSION)"'
	@echo '  }'

.PHONY: clean
clean:
	rm -f $(BINARY)

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: test
test:
	go test ./... -v -timeout 120s
