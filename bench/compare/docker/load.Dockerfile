# The speed pillar's load generator (docs/11 §4, §5.5): vegeta, an
# established open-loop tool, beside the harness binary for target lists and
# curl and jq for the origin's control API.
FROM golang:1.27.3-alpine AS build
RUN CGO_ENABLED=0 go install github.com/tsenart/vegeta/v12@v12.13.0
WORKDIR /src
COPY go.mod *.go ./
RUN CGO_ENABLED=0 go build -o /compare .

FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
RUN apk add --no-cache curl jq
COPY --from=build /go/bin/vegeta /usr/local/bin/vegeta
COPY --from=build /compare /usr/local/bin/compare
