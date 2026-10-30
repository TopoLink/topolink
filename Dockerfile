# Topolink — 纯 Go、无 cgo，多阶段构建
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /topolink ./cmd/topolink

FROM alpine:3.19
# git 用于 diff 驱动的爆炸半径模式；ca-certificates 用于调用 LLM HTTPS
RUN apk add --no-cache git ca-certificates
# 允许在挂载的 workspace 上执行 git（避免 dubious ownership）
RUN git config --system --add safe.directory '*'
COPY --from=build /topolink /usr/local/bin/topolink
ENTRYPOINT ["topolink"]
