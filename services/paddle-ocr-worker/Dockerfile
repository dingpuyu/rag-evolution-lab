FROM python:3.12-slim AS runtime
ARG DEBIAN_MIRROR=deb.debian.org
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PADDLE_PDX_MODEL_SOURCE=BOS
WORKDIR /app
RUN if [ "$DEBIAN_MIRROR" != "deb.debian.org" ]; then \
      sed -i "s|deb.debian.org|$DEBIAN_MIRROR|g" /etc/apt/sources.list.d/debian.sources; \
    fi \
    && apt-get update \
    && apt-get install -y --no-install-recommends libgomp1 libglib2.0-0 libgl1 \
    && rm -rf /var/lib/apt/lists/*
COPY services/paddle-ocr-worker/pyproject.toml ./pyproject.toml
RUN pip install --no-cache-dir .
COPY services/paddle-ocr-worker/app ./app
EXPOSE 8071
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8071"]
