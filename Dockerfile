FROM golang:alpine as builder
MAINTAINER Alexander Zillion <docker@alexzillion.com>

ENV PATH /go/bin:/usr/local/go/bin:$PATH
ENV GOPATH /go

RUN	apk add --no-cache \
	ca-certificates

COPY . /go/src/github.com/azillion/golint-fixer

RUN set -x \
	&& apk add --no-cache --virtual .build-deps \
		git \
		gcc \
		libc-dev \
		libgcc \
		make \
	&& cd /go/src/github.com/azillion/golint-fixer \
	&& make static \
	&& mv golint-fixer /usr/bin/golint-fixer \
	&& apk del .build-deps \
	&& rm -rf /go \
	&& echo "Build complete."

FROM alpine:latest

COPY --from=builder /usr/bin/golint-fixer /usr/bin/golint-fixer
COPY --from=builder /etc/ssl/certs/ /etc/ssl/certs

ENTRYPOINT [ "golint-fixer" ]
CMD [ "--help" ]
