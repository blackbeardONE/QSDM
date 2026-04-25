# Legacy Dockerfile — Major Update split-image notice
# =====================================================
# Starting with the Major Update (see QSDM/docs/docs/REBRAND_NOTES.md), the
# single-image build model is replaced by two explicit role-scoped images:
#
#   - Dockerfile.validator  : CPU-only Alpine image, validator_only build
#                             profile (refuses to run as a miner).
#   - Dockerfile.miner      : CUDA-enabled image, full build profile
#                             (includes pkg/mining/cuda).
#
# This file is preserved to remain build-compatible with existing CI and
# operator scripts that invoke `docker build -t qsdm:latest .`. It delegates
# to Dockerfile.validator because the validator profile is the smaller and
# safer default for operators who have not explicitly opted into mining.
#
# To build the miner image, run:
#
#   docker build -f Dockerfile.miner -t qsdm/miner:latest .
#
# To build the validator image explicitly:
#
#   docker build -f Dockerfile.validator -t qsdm/validator:latest .
#
# The inline build below intentionally mirrors Dockerfile.validator; keep the
# two in sync when modifying.

FROM golang:1.25-alpine AS builder

RUN apk update && apk add --no-cache \
    ca-certificates \
    gcc \
    musl-dev \
    cmake \
    make \
    git \
    sqlite-dev \
    openssl-dev

WORKDIR /tmp
RUN git clone --depth 1 https://github.com/open-quantum-safe/liboqs.git && \
    cd liboqs && \
    mkdir build && cd build && \
    cmake -DCMAKE_INSTALL_PREFIX=/usr/local -DBUILD_SHARED_LIBS=ON .. && \
    make -j$(nproc) && \
    make install && \
    rm -rf /tmp/liboqs

WORKDIR /app
COPY source ./source
RUN cd source && go mod download

# Default build here is validator_only so accidentally running the legacy
# image in a production environment does not silently attempt to mine.
RUN cd source && CGO_ENABLED=1 \
    CGO_CFLAGS="-I/usr/local/include" \
    CGO_LDFLAGS="-L/usr/local/lib -loqs -lssl -lcrypto" \
    go build -tags validator_only -o ../qsdm ./cmd/qsdm

FROM alpine:latest

RUN apk update && apk add --no-cache \
    ca-certificates \
    sqlite \
    libstdc++ \
    openssl \
    wget

COPY --from=builder /usr/local/lib/liboqs.so* /usr/local/lib/
COPY --from=builder /app/qsdm /usr/local/bin/qsdm

# Legacy binary name so existing CMD/scripts continue to work.
RUN ln -s /usr/local/bin/qsdm /usr/local/bin/qsdm

WORKDIR /app

EXPOSE 4001 8080 8081 9000

ENV CGO_ENABLED=1
ENV LD_LIBRARY_PATH=/usr/local/lib
ENV QSDM_NODE_ROLE=validator
ENV QSDM_MINING_ENABLED=false

HEALTHCHECK --interval=30s --timeout=10s --start-period=90s --retries=3 \
  CMD wget -q -O- http://127.0.0.1:8080/api/v1/health/live > /dev/null || exit 1

CMD ["qsdm"]
