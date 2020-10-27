FROM golang:1.15-buster AS builder
WORKDIR /go/src
COPY . .
RUN GO111MODULE=on go install .

FROM gcr.io/distroless/base-debian10
COPY --from=builder /go/bin/cloudmap-proxy /bin/cloudmap-proxy
ENTRYPOINT ["/bin/cloudmap-proxy"]
