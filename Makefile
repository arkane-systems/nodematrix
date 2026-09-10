VERSION ?= 0.1.0

.PHONY: build deb clean

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/nodematrix ./cmd/nodematrix

deb: build
	cd packaging && NODEMATRIX_VERSION=$(VERSION) nfpm package --config nfpm.yaml --packager deb --target ..

clean:
	rm -rf dist *.deb
