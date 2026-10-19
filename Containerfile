# syntax=docker.io/docker/dockerfile:1.27.0
# check=skip=InvalidDefaultArgInFrom

# No defaults, so a build that was not passed them fails instead of picking a
# version nothing else uses. Compose supplies them from project.versions.
ARG GO_VERSION
ARG GOLANGCI_LINT_VERSION

# COPY --from does not expand build arguments, so the pinned image needs a stage.
FROM docker.io/golangci/golangci-lint:${GOLANGCI_LINT_VERSION} AS golangci

# --- Development stage ---
# Tools only. The source is bind-mounted, so `go mod init` works before go.mod
# exists. gcc ships in the golang image and is required for -race.
FROM docker.io/library/golang:${GO_VERSION} AS dev

# An ARG before the first FROM reaches the FROM lines only, so these come again.
ARG GOTESTSUM_VERSION
# Must match the developer's uid on Linux, where the bind-mounted source keeps
# its host ownership. Baked in so the container needs neither root nor CAP_CHOWN.
ARG DEV_UID=65532
ARG DEV_GID=65532

COPY --from=golangci /usr/bin/golangci-lint /usr/local/bin/golangci-lint

RUN GOBIN=/usr/local/bin go install gotest.tools/gotestsum@${GOTESTSUM_VERSION} && \
	rm -rf /root/.cache /go/pkg && \
	mkdir -p /cache/go-build /cache/go-mod /cache/tmp /cache/golangci-lint /cache/home /src && \
	chown -R ${DEV_UID}:${DEV_GID} /cache /src

# After the RUN, so the root-owned tool install does not write into /cache.
# GOTMPDIR because /tmp is mounted noexec and test binaries must be executable.
ENV GOCACHE=/cache/go-build \
	GOMODCACHE=/cache/go-mod \
	GOTMPDIR=/cache/tmp \
	GOLANGCI_LINT_CACHE=/cache/golangci-lint \
	HOME=/cache/home \
	CGO_ENABLED=0 \
	GOOS=linux

USER ${DEV_UID}:${DEV_GID}
WORKDIR /src

LABEL org.opencontainers.image.title="skb (development)"
LABEL org.opencontainers.image.description="Go toolchain over the bind-mounted skb source"
