# Makefile for a conf-watcher project
#
# Author: Keontang <ikeontang@gmail.com>

#
# Go parameters
GO_CMD=go
GO_BUILD=$(GO_CMD) build
GO_TEST=$(GO_CMD) test
GO_CLEAN=$(GO_CMD) clean
GO_FMT=$(GO_CMD) fmt -x
# Vet is a simple checker for static errors in Go source code. 
GO_VET=$(GO_CMD) vet

GIT_TAG=`git describe --tags --always`
BUILD_TIME=`date +%FT%T%z`

LDFLAGS=-ldflags "-X github.com/caicloud/conf-watcher/version.GitTag=$(GIT_TAG) -X github.com/caicloud/conf-watcher/version.BuildTime=$(BUILD_TIME)"

DIST_PATH=dist

#
# Packages
PACKAGES := app/options app
PACKAGES_EXPANDED := $(PACKAGES:%=github.com/caicloud/conf-watcher/%)


.PHONY: all build clean fmt vet

all: build

build: vet fmt
ifneq ($(DIST_PATH), $(wildcard $(DIST_PATH)))
	mkdir -p $(DIST_PATH)
endif
	$(GO_BUILD) -o $(DIST_PATH)/conf-watcher $(LDFLAGS) .

clean:
	@for p in $(PACKAGES_EXPANDED); do \
		echo "==> Clean $$p ..."; \
		$(GO_CLEAN) $$p; \
	done
	rm -rf $(DIST_PATH)

test:
	@for p in $(PACKAGES_EXPANDED); do \
		echo "==> Unit Testing $$p ..."; \
		$(GO_TEST) $$p || exit 1; \
	done

fmt:
	@for p in $(PACKAGES_EXPANDED); do \
		echo "==> Formatting $$p ..."; \
		$(GO_FMT) $$p || exit 1; \
	done

vet:
	@for p in $(PACKAGES_EXPANDED); do \
		echo "==> Vet $$p ..."; \
		$(GO_VET) $$p; \
	done
