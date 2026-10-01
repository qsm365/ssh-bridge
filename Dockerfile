FROM node:22-bookworm-slim AS web-builder
WORKDIR /src/web
ARG NPM_REGISTRY=https://registry.npmjs.org
COPY web/package.json web/package-lock.json ./
RUN npm ci --registry="$NPM_REGISTRY"
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS go-builder
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-builder /src/web/dist /src/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ssh-bridge ./cmd/ssh-bridge

FROM debian:bookworm-slim
RUN mkdir -p /data /run/ssh-keys /etc/ssl/certs && chown 10001:10001 /data
COPY --from=go-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go-builder --chown=10001:10001 /out/ssh-bridge /usr/local/bin/ssh-bridge
USER 10001:10001
EXPOSE 7408
ENTRYPOINT ["/usr/local/bin/ssh-bridge"]
CMD ["--mode", "server", "--listen", "0.0.0.0:7408", "--data-dir", "/data"]
