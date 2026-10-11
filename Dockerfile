FROM docker.io/library/golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY generated/ generated/
COPY internal/ internal/
COPY cmd/ cmd/
RUN CGO_ENABLED=1 go build -tags sqlite_fts5 -trimpath -ldflags='-s -w' -o /out/nas-search ./cmd/nas-search

FROM docker.io/library/python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 ca-certificates && rm -rf /var/lib/apt/lists/*
COPY requirements.lock /app/requirements.lock
RUN pip install --no-cache-dir -r /app/requirements.lock && python -c 'import shutil,sqlite_vec; shutil.copyfile(sqlite_vec.loadable_path()+".so", "/opt/vec0.so")'
COPY --from=build /out/nas-search /app/nas-search
COPY worker/ /app/worker/
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 NAS_WORKER=/app/worker/worker.py NAS_VEC_EXTENSION=/opt/vec0.so
EXPOSE 8080
ENTRYPOINT ["/app/nas-search"]
