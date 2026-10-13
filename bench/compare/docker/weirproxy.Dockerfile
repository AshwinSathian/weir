# weirproxy as a user gets it: the published module, default settings.
FROM golang:1.27.2-alpine AS build
ARG WEIR_VERSION=v1.0.1
RUN CGO_ENABLED=0 GOBIN=/out go install github.com/AshwinSathian/weir/examples/weirproxy@${WEIR_VERSION}

FROM scratch
COPY --from=build /out/weirproxy /weirproxy
ENTRYPOINT ["/weirproxy"]
