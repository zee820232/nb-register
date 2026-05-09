FROM docker.m.daocloud.io/library/python:3.12-slim-bookworm

# Use domestic apt mirrors
RUN sed -i 's/deb.debian.org/mirrors.ustc.edu.cn/g' /etc/apt/sources.list.d/debian.sources

# --- System dependencies for Firefox/Camoufox + Xvfb ---
RUN apt-get update && apt-get install -y --no-install-recommends \
    xvfb \
    libgtk-3-0 \
    libasound2 \
    libx11-xcb1 \
    libgbm1 \
    libnss3 \
    libxcomposite1 \
    libxdamage1 \
    libxrandr2 \
    libxkbcommon0 \
    libdbus-glib-1-2 \
    libxt6 \
    fonts-noto-cjk \
    curl \
    && rm -rf /var/lib/apt/lists/*

# --- Python dependencies ---
COPY requirements.txt /tmp/requirements.txt
RUN pip config set global.index-url https://pypi.tuna.tsinghua.edu.cn/simple \
    && pip install --no-cache-dir -r /tmp/requirements.txt \
    && rm /tmp/requirements.txt

# --- Fetch Camoufox browser binary once in the dependency layer. Keep this
# before COPY . so normal application changes do not re-download 700MB.
RUN HTTP_PROXY=http://host.docker.internal:10809 HTTPS_PROXY=http://host.docker.internal:10809 python -m camoufox fetch

# --- Application ---
COPY . /app
WORKDIR /app

# --- Compile proto ---
RUN python -m grpc_tools.protoc -I proto --python_out=. --grpc_python_out=. proto/browser.proto

# Screenshot output
RUN mkdir -p /tmp/screenshots
ENV SCREENSHOT_DIR=/tmp/screenshots

# Default: gRPC service mode
ENV GRPC_PORT=50051
EXPOSE 50051

ENTRYPOINT ["python", "main.py", "serve"]
