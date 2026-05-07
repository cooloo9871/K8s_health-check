# syntax=docker/dockerfile:1.6
FROM golang:1.22-alpine AS builder

ENV CGO_ENABLED=0 GOOS=linux GOFLAGS="-trimpath"

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags="-s -w" -o /out/k8s-healthcheck ./

# ---- runtime ---------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="k8s-healthcheck" \
      org.opencontainers.image.description="Collects K8s cluster health data and emits a PDF report" \
      org.opencontainers.image.source="https://github.com/brobridge/k8s-health-check"

WORKDIR /
COPY --from=builder /out/k8s-healthcheck /k8s-healthcheck

USER 65532:65532
ENTRYPOINT ["/k8s-healthcheck"]
CMD ["--out=/reports"]
