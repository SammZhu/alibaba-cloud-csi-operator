# ── Build stage ──────────────────────────────────────────────────────────────────
# Use the official Go toolchain image for the build (multi-arch capable).
FROM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Cache dependency downloads before copying source.
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree.
COPY cmd/      cmd/
COPY api/      api/
COPY internal/ internal/

# Fully static binary — required for ubi-micro (no glibc).
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -a -ldflags="-s -w" -o manager cmd/main.go

# ── Runtime stage — Red Hat UBI9 Micro ───────────────────────────────────────────
# ubi9/ubi-micro is the smallest UBI image satisfying Red Hat Container Certification.
# It ships no package manager, shell, or libc — suitable only for fully static binaries.
FROM registry.access.redhat.com/ubi9/ubi-micro:latest

# ── Required labels for Red Hat Container Certification ──────────────────────────
# https://access.redhat.com/documentation/en-us/red_hat_software_certification
LABEL name="alibaba-cloud-csi-operator" \
      vendor="SammZhu" \
      version="v1.35.3" \
      release="1" \
      summary="OLM Operator for Alibaba Cloud CSI Driver on OpenShift External Platform" \
      description="Installs and manages Alibaba Cloud CSI drivers (Disk, NAS, OSS) on \
OpenShift clusters running in External Platform mode using RAM Role instance-principal \
authentication — no AK/SK required." \
      io.k8s.description="Installs and manages Alibaba Cloud CSI drivers on OpenShift \
clusters running in External Platform mode." \
      io.k8s.display-name="Alibaba Cloud CSI Operator" \
      io.openshift.tags="storage,csi,alibaba-cloud,openshift"

# LICENSE must be present in /licenses/ for Red Hat certification preflight checks.
COPY LICENSE /licenses/LICENSE

WORKDIR /

COPY --from=builder /workspace/manager .

# Run as non-root numeric UID 65532 (no /etc/passwd entry needed in ubi-micro).
USER 65532:65532

ENTRYPOINT ["/manager"]
