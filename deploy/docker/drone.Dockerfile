FROM golang:1.22 AS build
WORKDIR /src

COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY proto/ ./proto/
COPY gen/ ./gen/

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/drone ./cmd/drone

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/drone /drone
USER nonroot:nonroot
ENTRYPOINT ["/drone"]

