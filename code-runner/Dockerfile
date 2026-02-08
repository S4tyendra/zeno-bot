FROM debian:bookworm-slim

# Install Python, Node/Bun, and common tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 \
    python3-pip \
    python3-venv \
    curl \
    unzip \
    ca-certificates \
    imagemagick \
    ffmpeg \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Install Bun
RUN curl -fsSL https://bun.sh/install | bash
ENV PATH="/root/.bun/bin:$PATH"

# Install common Python packages
RUN pip3 install --break-system-packages \
    pillow \
    numpy \
    requests \
    httpx \
    colorthief \
    opencv-python-headless \
    MultiFeatures

# Create directories
RUN mkdir -p /workspace /generated /scripts

# Add helper scripts
COPY scripts/ /scripts/
RUN chmod +x /scripts/*

WORKDIR /workspace

# Keep container running
CMD ["tail", "-f", "/dev/null"]
