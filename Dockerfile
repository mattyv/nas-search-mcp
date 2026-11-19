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
    && mkdir -p /out/entities && cp -R custom_components/dreame_vacuum/dreame /out/dreame && cp LICENSE /out/LICENSE \
    && cp custom_components/dreame_vacuum/switch.py custom_components/dreame_vacuum/select.py \
          custom_components/dreame_vacuum/number.py custom_components/dreame_vacuum/time.py \
          custom_components/dreame_vacuum/button.py /out/entities/

# Build the vacuum environment where a compiler exists (netifaces ships no
# Python 3.12 wheel); the runtime image receives only the finished venv.
FROM docker.io/library/python:3.12-bookworm AS vacuum-deps
COPY vacuum/requirements.lock /tmp/vacuum-requirements.lock
RUN python -m venv /opt/vacuum-venv && /opt/vacuum-venv/bin/pip install --no-cache-dir -r /tmp/vacuum-requirements.lock

FROM docker.io/library/python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 ca-certificates poppler-utils tesseract-ocr tesseract-ocr-eng && rm -rf /var/lib/apt/lists/*
COPY requirements.lock /app/requirements.lock
RUN pip install --no-cache-dir -r /app/requirements.lock && python -c 'import shutil,sqlite_vec; shutil.copyfile(sqlite_vec.loadable_path()+".so", "/opt/vec0.so")'
# A separate environment keeps the vacuum libraries away from the search worker.
COPY --from=vacuum-deps /opt/vacuum-venv /opt/vacuum-venv
COPY --from=dreame /out/ /opt/dreame/
# Fail the build, not the NAS, if the library cannot load in the runtime image.
RUN /opt/vacuum-venv/bin/python -I -W ignore -c "import sys; sys.path.insert(0, '/opt/dreame'); import dreame, py_mini_racer; py_mini_racer.MiniRacer().eval('1+1'); print('vacuum library', dreame.VERSION)"
COPY --from=build /out/nas-search /app/nas-search
COPY worker/ /app/worker/
COPY vacuum/broker.py vacuum/catalogue.py /app/vacuum/
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 NAS_WORKER=/app/worker/worker.py NAS_VEC_EXTENSION=/opt/vec0.so
EXPOSE 8080
ENTRYPOINT ["/app/nas-search"]
