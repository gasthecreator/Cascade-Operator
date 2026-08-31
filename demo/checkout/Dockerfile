# Build context is demo/ (not the repo root) — see hack/deploy-demo.sh.
FROM golang:1.26 AS builder
WORKDIR /workspace
COPY go.mod ./
COPY internal/ internal/
COPY checkout/ checkout/
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/checkout ./checkout

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/checkout /checkout
USER 65532:65532
ENTRYPOINT ["/checkout"]
