FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /mcp ./cmd/mcp && \
    CGO_ENABLED=0 go build -trimpath -o /reqlog ./cmd/reqlog

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /mcp /mcp
COPY --from=build /reqlog /reqlog
ENTRYPOINT ["/mcp"]
