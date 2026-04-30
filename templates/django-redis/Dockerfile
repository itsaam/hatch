# syntax=docker/dockerfile:1.7
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1

RUN apt-get update && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN --mount=type=cache,target=/root/.cache/pip \
    pip install -r requirements.txt

COPY myproj/ ./myproj/
COPY manage.py .

EXPOSE 8000

CMD ["gunicorn", "myproj.wsgi:application", "--bind", "0.0.0.0:8000", "--workers", "2"]
