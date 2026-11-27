FROM moby/buildkit:v0.35.1@sha256:baf6138eb8c37644800c7c913fba051e840ebb9ba13eaf92d92ef9a1712d0b4c AS buildkit

FROM golang:1.27.4-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod ./
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/api .

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=buildkit /usr/bin/buildctl /usr/bin/buildctl
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
EXPOSE 8080
ENTRYPOINT ["/app/api"]
