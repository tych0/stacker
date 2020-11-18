GO_SRC=$(shell find . -name \*.go)
VERSION=$(shell git describe --tags || git rev-parse HEAD)
VERSION_FULL=$(if $(shell git status --porcelain --untracked-files=no),$(VERSION)-dirty,$(VERSION))
TEST?=$(patsubst test/%.bats,%,$(wildcard test/*.bats))
JOBS?=$(shell grep -c processor /proc/cpuinfo)
BATS?=bats

BUILD_TAGS = exclude_graphdriver_devicemapper containers_image_openpgp

stacker: $(GO_SRC) go.mod go.sum
	go build -tags "$(BUILD_TAGS)" -ldflags "-X main.version=$(VERSION_FULL)" -o stacker ./cmd

.PHONY: lint
lint: $(GO_SRC)
	go fmt ./... && ([ -z $(TRAVIS) ] || git diff --quiet)
	bash test/static-analysis.sh
	go test -tags "$(BUILD_TAGS)" ./...
	$(shell go env GOPATH)/bin/golangci-lint run --build-tags "$(BUILD_TAGS)"

check-%: stacker
	[ -d ./test/centos ] || skopeo --insecure-policy copy docker://centos:latest oci:./test/centos:latest
	[ -d ./test/ubuntu ] || skopeo --insecure-policy copy docker://ubuntu:latest oci:./test/ubuntu:latest
	sudo -E "PATH=$$PATH" STORAGE_TYPE=$(subst check-,,$@) $(BATS) --jobs "$(JOBS)" -t $(patsubst %,test/%.bats,$(TEST))

# make check TEST=basic will run only the basic test.
.PHONY: check
check: lint check-btrfs check-overlay

.PHONY: vendorup
vendorup:
	go get -u
	go mod tidy

.PHONY: clean
clean:
	-rm -r stacker
	-rm -r ./test/centos ./test/ubuntu
