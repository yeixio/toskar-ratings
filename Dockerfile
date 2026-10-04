# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/toskar-ratings ./cmd/toskar-ratings
# The data directory, owned by distroless's nonroot user so a new volume is
# writable.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/toskar-ratings /usr/local/bin/toskar-ratings
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME /data
EXPOSE 8080
ENV RATINGS_ADDR=:8080 RATINGS_DB=/data/ratings.db
USER nonroot
ENTRYPOINT ["/usr/local/bin/toskar-ratings"]
CMD ["serve"]
