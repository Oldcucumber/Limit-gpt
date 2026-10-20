# SPDX-License-Identifier: GPL-3.0-only
FROM golang:1.26-bookworm AS plugin
WORKDIR /src
COPY plugin/go.mod plugin/go.sum ./
RUN go mod download
COPY plugin/ ./
RUN go test ./... && CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o /out/limit-gpt.so .

FROM oven/bun:1.3.14 AS frontend
WORKDIR /src
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
ENV VERSION=limit-gpt-0.1.0
RUN bun run build

FROM scratch AS artifacts
COPY --from=plugin /out/limit-gpt.so /limit-gpt.so
COPY --from=frontend /src/dist/index.html /management.html
COPY config.example.yaml README_CN.md LICENSE THIRD_PARTY.md /
COPY licenses/ /licenses/
