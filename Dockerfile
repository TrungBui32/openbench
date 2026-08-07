# OpenBench orchestrator image: a single static binary plus the tools it
# shells out to (terraform; docker CLI + kubectl for operator use).

FROM golang:1.25 AS build
WORKDIR /src
COPY orchestrator/go.mod orchestrator/go.sum ./
RUN go mod download
COPY orchestrator/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/openbench ./cmd/openbench

FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl \
    && curl -fsSL -o /tmp/terraform.zip https://releases.hashicorp.com/terraform/1.9.8/terraform_1.9.8_linux_amd64.zip \
    && unzip -o /tmp/terraform.zip -d /usr/local/bin terraform && rm /tmp/terraform.zip
COPY --from=build /out/openbench /usr/local/bin/openbench
ENTRYPOINT ["openbench"]
CMD ["--help"]
