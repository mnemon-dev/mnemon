#!/bin/sh
set -eu

echo "Running Honcho database migrations..."
/app/.venv/bin/python scripts/provision_db.py

echo "Aligning Honcho vector columns with the configured embedding dimensions..."
/app/.venv/bin/python scripts/configure_embeddings.py --yes

echo "Starting Honcho API..."
exec /app/.venv/bin/fastapi run --host 0.0.0.0 src/main.py
