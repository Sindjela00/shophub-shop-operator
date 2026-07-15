# Placeholder multi-stage Dockerfile for the operator manager binary.
# TODO: implement once controller logic is in place.
FROM golang:1.22 AS build
WORKDIR /workspace

FROM gcr.io/distroless/static:nonroot
WORKDIR /
USER 65532:65532
ENTRYPOINT ["/manager"]
