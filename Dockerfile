FROM golang:1.17-alpine as builder

RUN apk add make

RUN mkdir /src

COPY . /src

WORKDIR /src

RUN make build

FROM alpine:latest

RUN mkdir /app

COPY --from=builder /src/bin/* /app

WORKDIR /app

EXPOSE 8442

CMD "./cf-proxy"