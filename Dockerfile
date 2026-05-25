# syntax=docker/dockerfile:1

FROM golang:1.25.10-alpine

WORKDIR /app

RUN apk add --no-cache make ca-certificates chromium ffmpeg

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	make build

CMD [ "./target/btcpp-web" ]
