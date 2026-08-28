# syntax=docker/dockerfile:1.7
FROM golang:1.27-alpine AS build
ARG TARGET=stormrelay-server
ARG VERSION=dev
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=true -ldflags="-s -w -X main.version=${VERSION}" -o /out/stormrelay ./cmd/${TARGET}

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S -g 65532 stormrelay && adduser -S -D -H -u 65532 -G stormrelay stormrelay
COPY --from=build /out/stormrelay /usr/local/bin/stormrelay
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/stormrelay"]
