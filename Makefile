BINARY := t0x-map
CMD := ./cmd/t0x-map
VERSION := $(shell tr -d '\n' < VERSION)
LDFLAGS := -s -w

LINUX_GOOS := linux
LINUX_GOARCH := amd64
LINUX_OUT := bin/$(BINARY)-$(LINUX_GOOS)-$(LINUX_GOARCH)
DIST_DIR := dist/$(BINARY)-$(VERSION)-$(LINUX_GOOS)-$(LINUX_GOARCH)

.PHONY: build build-linux dist test clean install

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

# Cross-compile from macOS (or anywhere) for gnssplot (Linux x86_64).
build-linux:
	CGO_ENABLED=0 GOOS=$(LINUX_GOOS) GOARCH=$(LINUX_GOARCH) \
		go build -ldflags "$(LDFLAGS)" -o $(LINUX_OUT) $(CMD)
	@file $(LINUX_OUT) || true
	@echo "Built $(LINUX_OUT) ($(VERSION))"

# Package binary + systemd units + example config for scp to the server.
dist: build-linux
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	cp $(LINUX_OUT) $(DIST_DIR)/$(BINARY)
	cp config.example.yaml $(DIST_DIR)/
	cp t0x-map.service t0x-map-index.service t0x-map-index.timer $(DIST_DIR)/
	cp -R deploy $(DIST_DIR)/
	cp README.md $(DIST_DIR)/
	tar -C dist -czf dist/$(BINARY)-$(VERSION)-$(LINUX_GOOS)-$(LINUX_GOARCH).tar.gz \
		$(BINARY)-$(VERSION)-$(LINUX_GOOS)-$(LINUX_GOARCH)
	@echo "Dist: dist/$(BINARY)-$(VERSION)-$(LINUX_GOOS)-$(LINUX_GOARCH).tar.gz"

test:
	go test ./...

clean:
	rm -rf bin/ dist/

# Run on the Linux host (after copying the linux binary to bin/t0x-map).
install:
	install -d /usr/local/bin /etc/t0x-map /var/lib/t0x-map
	install -m 755 bin/$(BINARY) /usr/local/bin/$(BINARY)
	@if [ ! -f /etc/t0x-map/config.yaml ]; then \
		install -m 644 config.example.yaml /etc/t0x-map/config.yaml; \
	else \
		echo "Keeping existing /etc/t0x-map/config.yaml"; \
	fi
	install -m 644 t0x-map.service /etc/systemd/system/t0x-map.service
	install -m 644 t0x-map-index.service /etc/systemd/system/t0x-map-index.service
	install -m 644 t0x-map-index.timer /etc/systemd/system/t0x-map-index.timer
	@echo "Installed $(BINARY) $(VERSION). Enable with:"
	@echo "  sudo systemctl daemon-reload"
	@echo "  sudo systemctl enable --now t0x-map.service"
	@echo "  sudo systemctl enable --now t0x-map-index.timer"
	@echo "  sudo t0x-map index -config /etc/t0x-map/config.yaml"
