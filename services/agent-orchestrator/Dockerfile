FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PATH="/app/.venv/bin:${PATH}"

WORKDIR /app
COPY services/agent-orchestrator/pyproject.toml services/agent-orchestrator/uv.lock /app/
RUN pip install --no-cache-dir uv \
    && uv sync --frozen --no-dev --no-install-project

COPY services/agent-orchestrator/app /app/app

EXPOSE 8090
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8090"]
