FROM docker.io/library/golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY generated/ generated/
COPY internal/ internal/
COPY cmd/ cmd/
RUN CGO_ENABLED=1 go build -tags sqlite_fts5 -trimpath -ldflags='-s -w' -o /out/nas-search ./cmd/nas-search

# Optional robot-vacuum service: Tasshack/dreame-vacuum's protocol library (MIT),
# pinned by commit. Only its standalone dreame/ package is used, not Home Assistant.
FROM docker.io/library/golang:1.25-bookworm AS dreame
ARG DREAME_COMMIT=fec086caf064f2681e8bcc7be20a0d0ef54f444d
RUN git init -q /src && cd /src && git fetch -q --depth 1 https://github.com/Tasshack/dreame-vacuum.git "$DREAME_COMMIT" \
    && git checkout -q FETCH_HEAD && [ "$(git rev-parse HEAD)" = "$DREAME_COMMIT" ] \
    && mkdir -p /out && cp -R custom_components/dreame_vacuum/dreame /out/dreame && cp LICENSE /out/LICENSE

FROM docker.io/library/python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 ca-certificates poppler-utils tesseract-ocr tesseract-ocr-eng && rm -rf /var/lib/apt/lists/*
COPY requirements.lock /app/requirements.lock
RUN pip install --no-cache-dir -r /app/requirements.lock && python -c 'import shutil,sqlite_vec; shutil.copyfile(sqlite_vec.loadable_path()+".so", "/opt/vec0.so")'
COPY vacuum/requirements.lock /app/vacuum/requirements.lock
# A separate environment keeps the vacuum libraries away from the search worker.
RUN python -m venv /opt/vacuum-venv && /opt/vacuum-venv/bin/pip install --no-cache-dir -r /app/vacuum/requirements.lock
COPY --from=dreame /out/ /opt/dreame/
COPY --from=build /out/nas-search /app/nas-search
COPY worker/ /app/worker/
COPY vacuum/broker.py /app/vacuum/broker.py
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 NAS_WORKER=/app/worker/worker.py NAS_VEC_EXTENSION=/opt/vec0.so
EXPOSE 8080
ENTRYPOINT ["/app/nas-search"]
