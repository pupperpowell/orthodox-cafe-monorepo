# 📡 Go Web Radio Backend – Technical Overview

## Objective

Design and implement a Go backend server for a simple web radio. Each stream endpoint applies a unique `ffmpeg` filter chain to audio and serves the processed MP3 output in real-time. Multiple clients can connect to the same endpoint and receive the exact same audio stream, similar in behavior to Icecast.

---

## 🔧 System Architecture

```
+------------+         +------------------+       +-----------------------+
|  React     | <-----> |     Go Server     | <--> |  MP3 Audio Directory  |
| Frontend   |  HTTP   |  (Stream Handler) |       +-----------------------+
+------------+         +------------------+
                                 |
                          +-------------+
                          |   ffmpeg     |
                          | (per stream) |
                          +-------------+
```

---

## 🔑 Key Components

### 1. **Stream Manager**

- Registry for all live stream endpoints.
- Starts and manages long-lived `ffmpeg` processes per stream.
- Routes audio output to all active clients per stream.

### 2. **FFmpeg Process Wrapper**

- Launches `ffmpeg` with input from a directory of MP3s (looped playback).
- Applies specific filter graphs per stream (gain, lowpass, highpass).
- Outputs live MP3 audio via stdout.

### 3. **Client Broadcaster**

- Tracks connected clients.
- Forwards live audio chunks to each client.
- Handles disconnections and stream backpressure.

### 4. **HTTP Stream Endpoints**

- Exposed at `/stream/{category}/{variation}`.
- Streams audio using HTTP chunked transfer encoding.
- Clients receive synchronized MP3 audio.

---

## 🎧 Audio Format

- **Format:** MP3 (`audio/mpeg`)
- **Container:** Raw MP3 stream over HTTP
- **Streaming Protocol:** Plain HTTP 1.1 with `Transfer-Encoding: chunked`

---

## 📁 Audio Input

- **Source:** A directory of nested directories of `.mp3` files.
- **Selection:** Stream-specific subdirectories.
- **Playback:** Loop all `.mp3` files recursively using `ffmpeg`.

Example file structure:

```
/audio/
  chanting/
    inside/
      chant1.mp3
      chant2.mp3
    outside/
      chantA.mp3
  rain/
    inside/
      rain1.mp3
    outside/
      rainX.mp3
  ambient/
    ambient1.mp3
    ambient2.mp3
```

---

## 🎛️ Stream Filters and Endpoints

| Endpoint                   | FFmpeg Filter Graph                                                         |
| -------------------------- | --------------------------------------------------------------------------- |
| `/stream/chanting/inside`  | `volume=1.0,highpass=f=300,lowpass=f=3000`                                  |
| `/stream/chanting/outside` | `volume=1.0,highpass=f=100,lowpass=f=1000`                                  |
| `/stream/rain/inside`      | `volume=0.5,highpass=f=300,lowpass=f=2500`                                  |
| `/stream/rain/outside`     | `volume=0.6` _(No filtering: highpass/lowpass disabled)_                    |
| `/stream/ambient`          | `volume=0.3[d0];volume=0.7[d1];volume=0.3[d2];volume=0.4[d3];amix=inputs=4` |

### Ambient Notes:

- Assumes four mono ambient tracks (crickets, doves, loons, chickadees) are mixed.
- Use labeled filter chains to apply per-track gain and then combine with `amix`.

---

## 🧪 Example `ffmpeg` Command

```bash
ffmpeg -re -stream_loop -1 -i input.mp3 \
-filter_complex "volume=0.5,highpass=f=300,lowpass=f=2500" \
-f mp3 pipe:1
```

Or for ambient with mix:

```bash
ffmpeg -re -stream_loop -1 \
-i crickets.mp3 -i doves.mp3 -i loons.mp3 -i chickadees.mp3 \
-filter_complex "[0]volume=0.3[d0];[1]volume=0.7[d1];[2]volume=0.3[d2];[3]volume=0.4[d3];[d0][d1][d2][d3]amix=inputs=4" \
-f mp3 pipe:1
```

---

## 🔨 Implementation Overview

### 1. Stream Manager

```go
type StreamManager struct {
    mu      sync.Mutex
    streams map[string]*StreamInstance
}
```

### 2. Stream Instance

```go
type StreamInstance struct {
    ffmpegCmd    *exec.Cmd
    stdout       io.ReadCloser
    clients      map[*ClientConn]chan []byte
    addClient    chan *ClientConn
    removeClient chan *ClientConn
    stop         chan struct{}
}
```

### 3. HTTP Stream Handler

```go
func streamHandler(w http.ResponseWriter, r *http.Request) {
    // Lookup stream config
    // Register client to StreamInstance
    // Set headers:
    w.Header().Set("Content-Type", "audio/mpeg")
    w.Header().Set("Connection", "keep-alive")
    w.Header().Set("Cache-Control", "no-cache")
    // Stream data
}
```

---

## 📊 Monitoring

The backend exposes Prometheus-compatible metrics at `/metrics` for monitoring with Grafana.

- **Endpoint:** `GET /metrics`
- **Format:** Prometheus text format

### Key Metrics

- `webradio_streams_active`: Gauge showing the current number of active streams.
- `webradio_clients_connected`: Gauge vector showing connected clients per stream.
- `webradio_http_requests_total`: Counter vector for total HTTP requests by method and path.

This allows for easy integration with a Grafana dashboard to visualize server load and activity.

---

## 🛠 Deployment via systemd (ignore for now)

Create a systemd unit file like the following:

```ini
[Unit]
Description=Go Web Radio Backend
After=network.target

[Service]
ExecStart=/usr/local/bin/web-radio-backend
WorkingDirectory=/srv/web-radio
Restart=on-failure
User=radio
Environment=PORT=8080
Environment=AUDIO_DIR=/srv/web-radio/audio

[Install]
WantedBy=multi-user.target
```

**Install & Start: (ignore for now)**

```bash
sudo cp web-radio-backend.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable web-radio-backend
sudo systemctl start web-radio-backend
```

---

Let me know if you'd like the actual Go implementation scaffolding or help converting the Web Audio API-style filters into full `ffmpeg` syntax.
