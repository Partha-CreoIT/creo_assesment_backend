# Native-arch code runner: Python 3, GCC (C) and OpenJDK.
# Runs as a long-lived container; the backend executes code via `docker exec`
# as the non-root `runner` user with no network and a tmpfs workdir.
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        python3 \
        gcc \
        libc6-dev \
        default-jdk-headless \
        coreutils \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -u 1001 runner
USER runner
WORKDIR /home/runner

CMD ["sleep", "infinity"]
