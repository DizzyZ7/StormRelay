FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/stormrelay-plugin-conformance ./cmd/stormrelay-plugin-conformance

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /out/stormrelay-plugin-conformance /usr/local/bin/stormrelay-plugin-conformance
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/stormrelay-plugin-conformance"]
