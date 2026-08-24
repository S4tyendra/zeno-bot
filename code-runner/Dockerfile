FROM alpine:3.20

# Install only essential tools (Alpine uses apk)
RUN apk add --no-cache \
    curl \
    jq

# Install standalone uv (doesn't need system python)
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:$PATH"

# Create directories
RUN mkdir -p /workspace /generated /scripts

# uv will automatically download a lightweight Python 3.12 build
RUN uv venv --python 3.12 /workspace/.venv
ENV VIRTUAL_ENV=/workspace/.venv
ENV PATH="/workspace/.venv/bin:$PATH"
# Alpine uses ash/sh instead of bash by default, but this syntax still works
RUN echo "source /workspace/.venv/bin/activate" > /etc/profile.d/venv.sh

# Install minimal Python packages
RUN uv pip install \
    requests \
    httpx \
    yt-dlp

# Add helper scripts
COPY scripts/ /scripts/
RUN chmod +x /scripts/*

WORKDIR /workspace

# Keep container running
CMD ["tail", "-f", "/dev/null"]
