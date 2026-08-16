FROM python:3.12-slim AS runtime
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /app
COPY services/document-parser/pyproject.toml ./pyproject.toml
RUN pip install --no-cache-dir .
COPY services/document-parser/app ./app
RUN useradd --system --uid 10001 ragparser
USER ragparser
EXPOSE 8070
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8070"]
