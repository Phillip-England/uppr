FROM golang:1.26 AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /usr/local/bin/uppr .

FROM debian:bookworm-slim

WORKDIR /app
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates git openssh-client \
	&& rm -rf /var/lib/apt/lists/*

COPY --from=build /usr/local/bin/uppr /usr/local/bin/uppr

ENV PORT=8787
EXPOSE 8787

CMD ["sh", "-c", "exec uppr serve --addr 0.0.0.0:${PORT} ."]
