# Web Radio Backend

A Go-based web radio server that streams MP3 audio with real-time FFmpeg processing.

## Features

- Multiple concurrent audio streams with different filter chains
- Real-time MP3 streaming via HTTP chunked transfer encoding
- FFmpeg integration for audio processing (volume, highpass, lowpass filters)
- Multi-client support with synchronized playback
- Graceful shutdown and client management
- Prometheus metrics for Grafana integration

## Available Streams

- `/stream/chanting/inside` - Chanting audio with indoor acoustics filter
- `/stream/chanting/outside` - Chanting audio with outdoor acoustics filter
- `/stream/rain/inside` - Rain audio with indoor acoustics filter
- `/stream/rain/outside` - Rain audio with minimal filtering
- `/stream/ambient` - Mixed ambient sounds (crickets, doves, loons, chickadees)

## Prerequisites

- Go 1.21 or later
- FFmpeg installed and available in PATH
- MP3 audio files in the `audio/` directory structure

## Audio Directory Structure

The server expects audio files to be organized as follows:

```
audio/
├── chanting/
│   └── *.mp3
├── rain/
│   └── *.mp3
└── ambient/
    ├── crickets.mp3
    ├── doves.mp3
    ├── loons.mp3
    └── chickadees.mp3
```

## Installation

1. Install dependencies:

```bash
go mod tidy
```

2. Ensure FFmpeg is installed:

```bash
ffmpeg -version
```

## Running the Server

### Development

```bash
go run main.go
```

### Production Build

```bash
go build -o web-radio-backend main.go
./web-radio-backend
```

## Configuration

The server can be configured using environment variables:

- `PORT` - Server port (default: 8080)
- `AUDIO_DIR` - Path to audio directory (default: ./audio)

Example:

```bash
PORT=3000 AUDIO_DIR=/path/to/audio go run main.go
```

## API Endpoints

### Stream Endpoints

- `GET /stream/{category}` - Stream audio for category
- `GET /stream/{category}/{variation}` - Stream audio for category/variation

### Utility Endpoints

- `GET /health` - Health check
- `GET /streams` - List available streams
- `GET /metrics` - Prometheus metrics for monitoring

## Monitoring with Grafana

The server exposes Prometheus-compatible metrics at the `/metrics` endpoint. You can use this to monitor the server's performance and status with Grafana.

### Exposed Metrics

- `webradio_streams_active`: The total number of active streams.
- `webradio_clients_connected`: The total number of connected clients per stream.
- `webradio_http_requests_total`: Total number of HTTP requests.

### Grafana Setup

1.  **Add a Prometheus Data Source**: In Grafana, add a new Prometheus data source and point it to your web radio server's `/metrics` endpoint (e.g., `http://localhost:8080/metrics`).
2.  **Create a Dashboard**: Create a new dashboard and add panels to visualize the metrics. For example, you can create a graph for `webradio_clients_connected` to see the number of listeners for each stream over time.

## Usage Examples

### Stream Audio

```bash
# Listen to rain with indoor acoustics
curl http://localhost:8080/stream/rain/inside

# Listen to ambient sounds
curl http://localhost:8080/stream/ambient
```

### Check Available Streams

```bash
curl http://localhost:8080/streams
```

### Health Check

```bash
curl http://localhost:8080/health
```

## FFmpeg Filter Chains

Each stream applies specific audio processing:

- **Chanting Inside**: `volume=1.0,highpass=f=300,lowpass=f=3000`
- **Chanting Outside**: `volume=1.0,highpass=f=100,lowpass=f=1000`
- **Rain Inside**: `volume=0.5,highpass=f=300,lowpass=f=2500`
- **Rain Outside**: `volume=0.6` (minimal filtering)
- **Ambient**: Multi-track mixing with individual volume controls

## Client Integration

The streams can be consumed by any HTTP client that supports chunked transfer encoding:

### HTML5 Audio

```html
<audio controls>
  <source src="http://localhost:8080/stream/rain/inside" type="audio/mpeg" />
</audio>
```

### JavaScript

```javascript
const audio = new Audio("http://localhost:8080/stream/ambient");
audio.play();
```

## Architecture

The server implements a multi-client streaming architecture:

1. **Stream Manager** - Manages active streams and FFmpeg processes
2. **Stream Instance** - Handles individual stream with client broadcasting
3. **Client Management** - Tracks connected clients and handles disconnections
4. **FFmpeg Integration** - Real-time audio processing and streaming

## Troubleshooting

### Common Issues

1. **FFmpeg not found**

   - Ensure FFmpeg is installed and in PATH
   - Test with: `ffmpeg -version`

2. **Audio files not found**

   - Check audio directory structure matches expected layout
   - Verify file permissions

3. **Stream not starting**
   - Check server logs for FFmpeg errors
   - Verify audio files are valid MP3 format

### Logs

The server provides detailed logging for:

- Stream startup/shutdown
- Client connections/disconnections
- FFmpeg process management
- Error conditions

## Performance Notes

- Each stream runs a separate FFmpeg process
- Clients receive synchronized audio chunks
- Slow clients are automatically disconnected to prevent backpressure
- Server supports graceful shutdown with proper cleanup
