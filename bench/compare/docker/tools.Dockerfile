# The harness binary: origin, runner and report (build context: bench/compare).
FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod *.go ./
RUN CGO_ENABLED=0 go build -o /compare .

FROM scratch
COPY --from=build /compare /compare
ENTRYPOINT ["/compare"]
