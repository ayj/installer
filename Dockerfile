FROM golang:alpine as builder

ENV PATH /go/bin:/usr/local/go/bin:$PATH
ENV GOPATH /go

RUN	apk add --no-cache \
	bash \
	git \
	ca-certificates

COPY . /go/src/istio.io/installer

# build operator
RUN set -x \
    && cd /go/src/istio.io/installer \
    && export GO111MODULE=on \
    && go build -o istio-operator istio-operator.go \
    && cp istio-operator /usr/bin/istio-operator \
    && rm -rf /go

# add helm
ENV HELM_VERSION="v2.14.2"
RUN set -x \
     && wget https://storage.googleapis.com/kubernetes-helm/helm-${HELM_VERSION}-linux-amd64.tar.gz \
     && tar -xvf helm-${HELM_VERSION}-linux-amd64.tar.gz \
     && mv linux-amd64/helm /usr/bin/helm

# add kubectl
ENV KUBECTL_VERSION="v1.15.2"
RUN set -x \
    && wget https://storage.googleapis.com/kubernetes-release/release/${KUBECTL_VERSION=}/bin/linux/amd64/kubectl \
    && chmod +x kubectl \
    && mv kubectl /usr/bin/kubectl

FROM alpine:latest

COPY --from=builder /usr/bin/istio-operator /usr/bin/istio-operator
COPY --from=builder /usr/bin/helm /usr/bin/helm
COPY --from=builder /usr/bin/kubectl /usr/bin/kubectl

ENTRYPOINT [ "istio-operator" ]
