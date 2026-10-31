FROM python:3.12-slim-bookworm

RUN mkdir -p /code/job /code/data
WORKDIR /code/job

# uv is pinned to .uv-version. The venv lives outside /code/job because compose
# bind-mounts ./src/job over it.
COPY --from=ghcr.io/astral-sh/uv:0.13.5 /uv /usr/local/bin/uv
ENV UV_PROJECT_ENVIRONMENT=/opt/venv \
    UV_PYTHON_DOWNLOADS=never \
    UV_NO_CACHE=1 \
    PATH="/opt/venv/bin:$PATH"

COPY pyproject.toml uv.lock .python-version /code/job/
RUN uv sync --frozen --no-dev

# Carrefour (strategy A) needs a real browser; Auchan (strategy B) does not,
# but the image ships chromium so any Playwright-based source works out of the box.
RUN playwright install --with-deps chromium

COPY src/job /code/job
