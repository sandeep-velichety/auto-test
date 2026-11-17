.PHONY: build ui test run demo clean

build: ui
	go build -o bin/testplane ./cmd/testplane

# Builds the React app into web/dist, which the Go binary embeds.
ui:
	cd web && npm install --no-audit --no-fund && npm run build

test:
	go test ./...

run: build
	./bin/testplane serve --workspace examples/workspace

demo: build
	./bin/testplane demo --workspace examples/workspace

clean:
	rm -rf bin
