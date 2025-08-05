APP_NAME = btcpp-web

.PHONY: dev-run
dev-run: build-all
	air -build.bin target/$(APP_NAME) -build.cmd="make build-all"

.PHONY: build
build:
	go build -v -o target/$(APP_NAME) ./cmd/web/main.go

.PHONY: css-build
css-build:
	tailwindcss -i templates/css/input.css -o static/css/mini.css --minify

.PHONY: png-conv
png-conv:
	cd media && ./convert.sh $(conf) $(subdir) && cd ..

.PHONY: build-all
build-all: build css-build

.PHONY: clean
clean:
	rm -f target/*
