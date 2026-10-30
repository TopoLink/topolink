# Topolink — multi-stage build.
# tree-sitter grammars are C code, so the build stage needs cgo + a C toolchain.
FROM golang:1.23-alpine AS build
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /topolink ./cmd/topolink

FROM alpine:3.19
# git for diff mode; ca-certificates for LLM HTTPS
RUN apk add --no-cache git ca-certificates
# allow git to operate on the mounted workspace (avoids dubious ownership)
RUN git config --system --add safe.directory '*'
COPY --from=build /topolink /usr/local/bin/topolink
ENTRYPOINT ["topolink"]
