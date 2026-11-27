FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /mcp ./cmd/mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /mcp /mcp
ENTRYPOINT ["/mcp"]
