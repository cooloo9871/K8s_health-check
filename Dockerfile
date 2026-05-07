# syntax=docker/dockerfile:1.6
FROM docker.io/library/golang:1.22-alpine AS builder

ENV CGO_ENABLED=0 GOOS=linux GOFLAGS="-trimpath"

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags="-s -w" -o /out/k8s-healthcheck ./

# ---- runtime ---------------------------------------------------------
# Using the :debug-nonroot variant so the image ships with busybox
# (tar, sh, cat, ...). kubectl cp shells into the container and runs
# `tar cf -`, so the static distroless image cannot be copied out of.
FROM gcr.io/distroless/base-debian12:debug-nonroot

LABEL org.opencontainers.image.title="k8s-healthcheck" \
      org.opencontainers.image.description="Collects K8s cluster health data and emits a PDF report"

WORKDIR /
COPY --from=builder /out/k8s-healthcheck /k8s-healthcheck

USER 65532:65532
ENTRYPOINT ["/k8s-healthcheck"]
CMD ["--out=/reports"]
