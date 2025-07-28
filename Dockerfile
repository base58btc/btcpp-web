# syntax=docker/dockerfile:1

FROM golang:1.22-alpine

WORKDIR /app

RUN apk update && \
	apk add --no-cache make

COPY . .
RUN go mod download
RUN make build

RUN apk --no-cache add ca-certificates
RUN apk --no-cache add chromium

CMD [ "./target/btcpp-web" ]
