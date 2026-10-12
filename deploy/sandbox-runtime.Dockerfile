# This is the workload image, separate from the Sandbox HTTP broker image.
ARG NODE_BASE=node:24-alpine
FROM golang:1.27.2-alpine AS guard
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY cmd/sandbox-guard/ cmd/sandbox-guard/
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags='-s -w' -o /guard ./cmd/sandbox-guard

FROM ${NODE_BASE}
COPY --from=guard /guard /usr/local/bin/agentos-sandbox-guard
LABEL org.agentos.sandbox.guard="1"
ENTRYPOINT ["/usr/local/bin/agentos-sandbox-guard"]
