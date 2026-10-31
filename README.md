# vgalary

A local image and video search app written in Go. vgalary scans folders and nested ZIP/RAR archives, embeds media through a multimodal llama.cpp server running EmbeddingGemma 2, and searches the vectors in Qdrant. The web UI supports natural language search, video timestamps, downloads, exports, directory management, indexing jobs, and connection settings.

The UI is available in **English, Korean, Simplified Chinese, Traditional Chinese, and Japanese**. It follows your browser language on first use, with English as the fallback. Use the language selector to override it; your choice is stored in that browser. Switching languages preserves your search and unsaved settings. Filenames, paths, model IDs, and server diagnostics remain unchanged.

## Requirements

To build from source:

- Git
- Node.js 22.13 or later and npm
- Go 1.27.3 or later

To index media:

- `ffmpeg` and `ffprobe` on your PATH, or configured in the app
- A llama.cpp server with multimodal embedding support
- Qdrant

The backend has been verified on Windows ARM64 with FFmpeg 9.0.2.

## Run locally

Start Qdrant using Docker Compose:

```sh
docker compose up -d
```

If Docker is unavailable, WSLC is an alternative:

```sh
wslc run --detach --name vgalary-qdrant --publish 127.0.0.1:6333:6333 --volume vgalary-qdrant-data:/qdrant/storage qdrant/qdrant:v1.20.0
```

Build the web UI and start the Go server:

```sh
npm ci --prefix web
npm run build --prefix web
go run ./cmd/vgalary -data-dir .local/app
```

Open [http://127.0.0.1:8181](http://127.0.0.1:8181). The defaults are:

| Service | Default |
| --- | --- |
| Web UI and API | `http://127.0.0.1:8181` |
| Embedding API | `http://127.0.0.1:18182/v1` |
| Embedding model | `embed` |
| Qdrant REST API | `http://127.0.0.1:6333` |

Connections and indexing directories are stored in SQLite and can be changed through the UI or API. Without `-data-dir`, the app uses `vgalary` inside the OS user configuration directory: `%APPDATA%` on Windows, `~/Library/Application Support` on macOS, and `$XDG_CONFIG_HOME` (or `~/.config`) on Linux. Use each data directory with one app process at a time.

You can also build and launch in one command. The web build is embedded into the Go executable:

```sh
node scripts/deploy.mjs
```

## Install and update

[The installation site](https://vgalary.saturday.ne.kr) is hosted on Cloudflare Pages and distributes **installation instructions and scripts only**. Use `install.ps1` on Windows or `install.sh` on macOS/Linux. These scripts clone, build, install, and update the app in your user directory; no OS detection is needed.

The app runs on your own computer or server. You decide whether and how to expose it outside your machine. See [installation and deployment](docs/deployment.md) for installation paths, Pages build settings, and the optional authenticated domain mode. The linked technical documents are currently in Korean.

## Index and search

In the UI, add a directory under **Directories**, start indexing, and check **Jobs** for progress. Search becomes available after a successful index. The library loads 24 items at a time through server-side pagination; use **Load more** for the next page.

Equivalent PowerShell API calls:

```powershell
$base = 'http://127.0.0.1:8181'
$jsonType = 'application/json; charset=utf-8'

# Register a directory accessible to the server.
$source = Invoke-RestMethod "$base/api/sources" -Method Post -ContentType $jsonType `
    -Body (@{ root_path = 'D:/media' } | ConvertTo-Json)

# Start indexing and check the returned job ID.
$job = Invoke-RestMethod "$base/api/sources/$($source.id)/scan" -Method Post `
    -ContentType $jsonType -Body '{"force":false}'
Invoke-RestMethod "$base/api/jobs/$($job.id)"

# Search once the job status is succeeded.
Invoke-RestMethod "$base/api/search" -Method Post -ContentType $jsonType `
    -Body (@{ query = 'a blue cat toy held in a hand'; limit = 10 } | ConvertTo-Json)

# Stop indexing this directory, exclude it from search, and clean up its index.
# Original files are preserved.
Invoke-RestMethod "$base/api/sources/$($source.id)" -Method Delete
```

An indexing job has **no overall time limit**. Individual external HTTP requests default to 180 seconds and FFmpeg/ffprobe processes to 600 seconds. You can cancel jobs manually. Previous `job_timeout_seconds` settings are ignored.

## Video indexing

Videos are embedded frame by frame. Search results group matches by video and return the best matching frame with `timestamp_ms` and a display timestamp. For H.264/H.265, the backend examines packet NAL units to select IDR frames. Codecs without IDR frames, such as AV1 and VP9, require explicit `processing.allow_keyframe_fallback` to use keyframes instead; `selection_kind` identifies the selection method. Fallback is disabled by default.

The first candidate frame is always kept. Later frames are embedded **only when they differ enough from the last selected frame**. The comparison uses normalized mean RGB differences on 64×64 images. `processing.video_frame_change_threshold` defaults to `0.12`; larger values require larger changes, and `0` disables filtering. Skipped frames do not call the embedding model. Original timestamps are preserved, and selected frames after the first record a `change_score`. Rescan directories after changing the frame policy.

## Folders and archives

Traversal has no fixed nesting-depth limit. The scanner detects directory-link cycles through ancestor identity and archive cycles through repeated ancestor hashes. If resource limits or read errors make a scan incomplete, existing unseen media is retained.

Archive entries can be shown using their paths and video timestamps without extracting them again for previews. Cached thumbnails are used when available. Explicit downloads and exports extract the selected entry through its nested archive chain.

## Configuration

API key settings accept **environment variable names only**, never the key values themselves. Leave the reference blank to connect without authentication. URLs and model names support both saved values and optional environment variable references. A referenced variable that is missing or empty is an error.

Changes to process environment variables require a restart. Settings saved in the app apply to new jobs; running jobs keep their settings snapshot. Changing models or frame policies requires reindexing. Search keeps using the active index until the new profile is ready across the registered directories.

## Diagnostics

The server writes JSON diagnostics to stderr at `info` level by default. Job lifecycle events, dependency retries, failures, and progress observations every 30 seconds include IDs for correlation. Normal file processing and successful calls are available at `debug`. Log messages are independent of the UI language.

```sh
go run ./cmd/vgalary -log-level debug -log-format text
```

The installed executable accepts the same flags. `-log-level` supports `debug`, `info`, `warn`, and `error`; `-log-format` supports `json` and `text`. Source-relative file and archive paths are omitted unless you explicitly enable `-log-paths`. Secrets, search text, request/response bodies, and absolute media paths are excluded. HTTP responses include a server-generated `X-Request-ID`.

Output uses a bounded queue and may be dropped if the sink is slow or unavailable. The app does not create or rotate log files; use stderr redirection or your service manager to collect them. SQLite remains the recovery authority. See the [logging design and verification record](docs/logging-plan.md) for events, limits, and tested behavior.

## Development and verification

```sh
go mod tidy
node --test scripts/install.test.mjs
npm run lint --prefix web
npm test --prefix web
npm run build --prefix web
go build ./...
go vet ./...
go test ./...
golangci-lint run --no-config ./...
govulncheck ./...
```

Install the vulnerability scanner separately with `go install golang.org/x/vuln/cmd/govulncheck@v1.9.0` and make sure your Go binary directory is on PATH. This does not add a tool dependency to the app module. See the [official govulncheck guide](https://go.dev/doc/tutorial/govulncheck). The 2026-10-31 scan found no known vulnerabilities for the Go 1.27.3 Windows ARM64 build configuration; see the [verification record](docs/backend-verification.md).

UI translations are centralized in `web/src/messages.ts`. Korean source messages have English, Simplified Chinese, Traditional Chinese, and Japanese translations. Keep named placeholders identical in every language. Locale detection, interpolation, and formatting live in `web/src/i18n.ts`; translation tests validate catalog completeness and placeholders.

To generate local media fixtures without overwriting an existing output directory:

```powershell
./scripts/make-local-fixtures.ps1 -ImageDirectory 'D:/sample-images' `
    -VideoPath 'D:/sample-videos/sample.webm'
```

To opt into integration tests against real llama.cpp and Qdrant services:

```powershell
$env:VGALARY_LIVE_TEST = '1'
$env:VGALARY_FIXTURE_ROOT = (Resolve-Path 'testdata/local/media').Path
go test ./internal/backend -run '^TestLiveBackend$' -v -count=1 -timeout 25m
```

Live tests use separate app data and collections, and remove their test collections afterward. With `VGALARY_TEST_DATA_DIR`, settings, index state, and `verification.json` are retained at the specified path. Regular `go test` skips live-service tests; when FFmpeg is available, it runs short H.264/H.265 detection tests. Local test media, databases, and caches are excluded from Git.

## Supported formats and current limits

Image candidates: JPEG, PNG, GIF, and WebP. GIF uses its first frame. Video candidates: MP4, MKV, WebM, MOV, AVI, M4V, and TS; decoding depends on your installed FFmpeg.

The app does not yet support virtual link resolution inside archives, encrypted or split archives, OCR or audio search, or transcoding originals for browser playback. Automatic total-cache management and cleanup of temporary files left by abnormal exits remain future work.

[API and settings](docs/api.md) · [Deployment and web development](docs/deployment.md) · [Backend verification](docs/backend-verification.md) · [Project blueprint](docs/blueprint.md)
