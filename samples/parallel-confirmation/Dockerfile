FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY sdk ./sdk
COPY samples/parallel-confirmation/go.mod samples/parallel-confirmation/go.sum ./samples/parallel-confirmation/
WORKDIR /src/samples/parallel-confirmation
RUN go mod download
COPY samples/parallel-confirmation ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM scratch
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="org parallel confirmation Worker sample" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.source="https://github.com/wu8685/org"
COPY --from=build /out/worker /worker
USER 65532:65532
ENTRYPOINT ["/worker"]
