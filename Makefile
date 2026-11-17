.PHONY: help check prereqs build build-go ui test run demo clean

help:
	@echo "make check     verify Go, Node.js and npm are installed and recent enough"
	@echo "make prereqs   install whatever make check reports missing (asks first; YES=1 to skip the question)"
	@echo "make build     build the UI, then bin/testplane (needs Go and Node.js)"
	@echo "make build-go  build bin/testplane with the UI already in web/dist (needs only Go)"
	@echo "make ui        rebuild the UI into web/dist (needs Node.js)"
	@echo "make test      run the Go tests"
	@echo "make run       build, then serve the example workspace on http://127.0.0.1:8080"
	@echo "make demo      build, then start the sample system the examples test against"
	@echo "make clean     remove bin/"

check:
	@sh scripts/check-prereqs.sh all

# Installs missing Go / Node.js. Uses Homebrew when present, otherwise the
# official downloads into ~/.local (no sudo). DRY_RUN=1 only shows the plan.
prereqs:
	@sh scripts/install-prereqs.sh

build: check ui build-go

# Uses the UI committed in web/dist, so Node.js is not needed.
build-go:
	@sh scripts/check-prereqs.sh go
	go build -o bin/testplane ./cmd/testplane

# Builds the React app into web/dist, which the Go binary embeds.
ui:
	@sh scripts/check-prereqs.sh ui
	cd web && npm install --no-audit --no-fund && npm run build

test:
	@sh scripts/check-prereqs.sh go
	go test ./...

run: build
	./bin/testplane serve --workspace examples/workspace

demo: build
	./bin/testplane demo --workspace examples/workspace

clean:
	rm -rf bin
