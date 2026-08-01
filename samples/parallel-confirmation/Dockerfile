FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM scratch
ARG VERSION=dev
ARG COMMIT=unknown
ARG SOURCE_REPOSITORY=https://github.com/wu8685/org-sample-parallel-confirmation
LABEL org.opencontainers.image.title="org parallel confirmation Worker sample" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.source="${SOURCE_REPOSITORY}"
COPY --from=build /out/worker /worker
USER 65532:65532
ENTRYPOINT ["/worker"]
