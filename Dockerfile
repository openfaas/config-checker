FROM ghcr.io/openfaas/license-check:0.4.1 as license-check

FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:1.18 as build

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

ARG VERSION
ARG GIT_COMMIT

ENV CGO_ENABLED=0
ENV GO111MODULE=on
ENV GOFLAGS=""

COPY --from=license-check /license-check /usr/bin/

WORKDIR /go/src/github.com/openfaas/checker

COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download

COPY . .

RUN license-check -path /go/src/github.com/openfaas/checker/ --verbose=false "Alex Ellis" "OpenFaaS Author(s)"
RUN gofmt -l -d $(find . -type f -name '*.go' -not -path "./vendor/*")

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
        --ldflags "-s -w " -a -installsuffix cgo -o checker .

LABEL org.label-schema.license="MIT" \
      org.label-schema.vcs-url="https://github.com/openfaas/checker" \
      org.label-schema.vcs-type="Git" \
      org.label-schema.name="openfaas/checker" \
      org.label-schema.vendor="openfaas" \
      org.label-schema.docker.schema-version="1.0"

FROM --platform=${BUILDPLATFORM:-linux/amd64} gcr.io/distroless/static:nonroot as ship
WORKDIR /
USER nonroot:nonroot

ENV http_proxy      ""
ENV https_proxy     ""

COPY --from=build /go/src/github.com/openfaas/checker/checker    .
CMD ["/checker"]
