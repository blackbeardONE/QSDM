# Portable CPU image (small, finishes quickly). AI proof uses torch CPU; tensor proof uses stub unless CUDA torch.
# GPU + FP16 matmul: use Dockerfile.ngc, nvcr.io login, then docker run --gpus all.
FROM python:3.11-slim-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cpu

WORKDIR /app
COPY validator_phase1.py gossip_daemon.py /app/
ENV PYTHONUNBUFFERED=1

CMD ["python3", "/app/validator_phase1.py"]
