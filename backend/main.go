package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// StreamConfig defines the configuration for each stream endpoint
type StreamConfig struct {
	AudioPath   string   // Path to audio files (relative to audio directory)
	FilterGraph string   // FFmpeg filter graph
	InputFiles  []string // List of input files for multi-input streams
}

var (
	streamsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webradio_streams_active",
		Help: "The total number of active streams.",
	})
	clientsConnected = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "webradio_clients_connected",
		Help: "The total number of connected clients per stream.",
	}, []string{"stream"})
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webradio_http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"method", "path"})
)

// ClientConn represents a connected client
type ClientConn struct {
	ID       string
	Response http.ResponseWriter
	Request  *http.Request
	Done     chan struct{}
}

// StreamInstance manages a single stream with its ffmpeg process and connected clients
type StreamInstance struct {
	Config    StreamConfig
	streamKey string
	ffmpegCmd *exec.Cmd
	stdout    io.ReadCloser
	clients   map[*ClientConn]chan []byte
	stop      chan struct{}
	mu        sync.RWMutex
	running   bool
}

// StreamManager manages all active streams
type StreamManager struct {
	mu       sync.RWMutex
	streams  map[string]*StreamInstance
	audioDir string
}

// Global stream configurations
var streamConfigs = map[string]StreamConfig{
	"chanting/inside": {
		AudioPath:   "chanting",
		FilterGraph: "volume=1.0,highpass=f=300,lowpass=f=3000",
	},
	"chanting/outside": {
		AudioPath:   "chanting",
		FilterGraph: "volume=1.0,highpass=f=100,lowpass=f=1000",
	},
	"rain/inside": {
		AudioPath:   "rain",
		FilterGraph: "volume=0.5,highpass=f=300,lowpass=f=2500",
	},
	"rain/outside": {
		AudioPath:   "rain",
		FilterGraph: "volume=0.6",
	},
	"ambient": {
		AudioPath:   "ambient",
		FilterGraph: "[0]volume=0.3[d0];[1]volume=0.7[d1];[2]volume=0.3[d2];[3]volume=0.4[d3];[d0][d1][d2][d3]amix=inputs=4",
		InputFiles:  []string{"crickets.mp3", "doves.mp3", "loons.mp3", "chickadees.mp3"},
	},
}

// NewStreamManager creates a new stream manager
func NewStreamManager(audioDir string) *StreamManager {
	return &StreamManager{
		streams:  make(map[string]*StreamInstance),
		audioDir: audioDir,
	}
}

// GetOrCreateStream gets an existing stream or creates a new one
func (sm *StreamManager) GetOrCreateStream(streamKey string) (*StreamInstance, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if stream already exists
	if stream, exists := sm.streams[streamKey]; exists {
		return stream, nil
	}

	// Get stream configuration
	config, exists := streamConfigs[streamKey]
	if !exists {
		return nil, fmt.Errorf("unknown stream: %s", streamKey)
	}

	// Create new stream instance
	stream := &StreamInstance{
		Config:    config,
		streamKey: streamKey,
		clients:   make(map[*ClientConn]chan []byte),
		stop:      make(chan struct{}),
	}

	// Start the stream
	if err := stream.Start(sm.audioDir, streamKey); err != nil {
		return nil, fmt.Errorf("failed to start stream: %v", err)
	}

	sm.streams[streamKey] = stream
	streamsActive.Inc()
	return stream, nil
}

// Start starts the stream instance with ffmpeg process
func (si *StreamInstance) Start(audioDir, streamKey string) error {
	si.mu.Lock()
	defer si.mu.Unlock()

	if si.running {
		return nil
	}

	// Build ffmpeg command
	cmd, err := si.buildFFmpegCommand(audioDir)
	if err != nil {
		return err
	}

	si.ffmpegCmd = cmd
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %v", err)
	}
	si.stdout = stdout

	// Start ffmpeg process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %v", err)
	}

	si.running = true

	// Start goroutine for broadcasting
	go si.broadcastAudio()

	log.Printf("Started stream with ffmpeg process PID: %d", cmd.Process.Pid)
	return nil
}

// buildFFmpegCommand constructs the ffmpeg command based on stream configuration
func (si *StreamInstance) buildFFmpegCommand(audioDir string) (*exec.Cmd, error) {
	audioPath := filepath.Join(audioDir, si.Config.AudioPath)

	var args []string

	// Handle multi-input streams (like ambient)
	if len(si.Config.InputFiles) > 0 {
		args = append(args, "-re", "-stream_loop", "-1")
		for _, file := range si.Config.InputFiles {
			inputPath := filepath.Join(audioPath, file)
			if _, err := os.Stat(inputPath); os.IsNotExist(err) {
				return nil, fmt.Errorf("input file not found: %s", inputPath)
			}
			args = append(args, "-i", inputPath)
		}
	} else {
		// Single input or directory of files
		// Get all MP3 files in the directory for cycling
		files, err := filepath.Glob(filepath.Join(audioPath, "*.mp3"))
		if err != nil || len(files) == 0 {
			return nil, fmt.Errorf("no MP3 files found in %s", audioPath)
		}

		if len(files) == 1 {
			// Single file - use simple loop
			args = append(args, "-re", "-stream_loop", "-1", "-i", files[0])
		} else {
			// Multiple files - create a playlist approach using concat demuxer
			// Create temporary playlist content with absolute paths
			playlistContent := ""
			for _, file := range files {
				// Use absolute paths in playlist to avoid path resolution issues
				absPath, err := filepath.Abs(file)
				if err != nil {
					return nil, fmt.Errorf("failed to get absolute path for %s: %v", file, err)
				}
				playlistContent += fmt.Sprintf("file '%s'\n", absPath)
			}

			// Create a temporary playlist file
			playlistPath := filepath.Join(os.TempDir(), fmt.Sprintf("playlist_%d.txt", time.Now().UnixNano()))
			if err := os.WriteFile(playlistPath, []byte(playlistContent), 0644); err != nil {
				return nil, fmt.Errorf("failed to create playlist file: %v", err)
			}

			// Schedule cleanup of playlist file
			go func() {
				time.Sleep(1 * time.Minute) // Give ffmpeg time to start
				os.Remove(playlistPath)
			}()

			// Use concat demuxer with infinite loop
			args = append(args, "-re", "-f", "concat", "-safe", "0", "-stream_loop", "-1", "-i", playlistPath)
		}
	}

	// Add filter complex if specified
	if si.Config.FilterGraph != "" {
		args = append(args, "-filter_complex", si.Config.FilterGraph)
	}

	// Output format
	args = append(args, "-f", "mp3", "pipe:1")

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = os.Stderr // For debugging
	return cmd, nil
}

// broadcastAudio reads from ffmpeg stdout and broadcasts to all clients
func (si *StreamInstance) broadcastAudio() {
	defer func() {
		if si.ffmpegCmd != nil && si.ffmpegCmd.Process != nil {
			si.ffmpegCmd.Process.Kill()
		}
	}()

	reader := bufio.NewReader(si.stdout)
	buffer := make([]byte, 4096)

	for {
		select {
		case <-si.stop:
			return
		default:
			n, err := reader.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Printf("Error reading from ffmpeg: %v", err)
				}
				return
			}

			if n > 0 {
				data := make([]byte, n)
				copy(data, buffer[:n])

				si.mu.RLock()
				for client, ch := range si.clients {
					select {
					case ch <- data:
						// Successfully sent
					default:
						// Channel is full, client is slow - disconnect them
						log.Printf("Client %s is slow, disconnecting", client.ID)
						go func(c *ClientConn) {
							si.RemoveClient(c, si.streamKey)
						}(client)
					}
				}
				si.mu.RUnlock()
			}
		}
	}
}

// AddClient adds a new client to the stream
func (si *StreamInstance) AddClient(client *ClientConn, streamKey string) {
	si.mu.Lock()
	si.clients[client] = make(chan []byte, 100)
	si.mu.Unlock()
	clientsConnected.WithLabelValues(streamKey).Inc()
	log.Printf("Client %s connected to stream", client.ID)
}

// RemoveClient removes a client from the stream
func (si *StreamInstance) RemoveClient(client *ClientConn, streamKey string) {
	si.mu.Lock()
	if ch, exists := si.clients[client]; exists {
		close(ch)
		delete(si.clients, client)
		clientsConnected.WithLabelValues(streamKey).Dec()
	}
	si.mu.Unlock()
	log.Printf("Client %s disconnected from stream", client.ID)
}

// Stop stops the stream instance
func (si *StreamInstance) Stop() {
	si.mu.Lock()
	defer si.mu.Unlock()

	if !si.running {
		return
	}

	close(si.stop)
	if si.ffmpegCmd != nil && si.ffmpegCmd.Process != nil {
		si.ffmpegCmd.Process.Kill()
	}
	si.running = false
	streamsActive.Dec()
}

// streamHandler handles HTTP requests for audio streams
func (sm *StreamManager) streamHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	category := vars["category"]
	variation := vars["variation"]

	var streamKey string
	if variation != "" {
		streamKey = category + "/" + variation
	} else {
		streamKey = category
	}

	// Get or create stream
	stream, err := sm.GetOrCreateStream(streamKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("Stream not available: %v", err), http.StatusNotFound)
		return
	}

	// Set headers for audio streaming
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Create client connection
	clientID := fmt.Sprintf("%s-%d", r.RemoteAddr, time.Now().UnixNano())
	client := &ClientConn{
		ID:       clientID,
		Response: w,
		Request:  r,
		Done:     make(chan struct{}),
	}

	// Add client to stream
	stream.AddClient(client, streamKey)
	defer stream.RemoveClient(client, streamKey)

	// Get client channel
	stream.mu.RLock()
	clientChan, exists := stream.clients[client]
	stream.mu.RUnlock()

	if !exists {
		http.Error(w, "Failed to register client", http.StatusInternalServerError)
		return
	}

	// Stream audio data to client
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Send initial flush to establish connection
	flusher.Flush()

	for {
		select {
		case data, ok := <-clientChan:
			if !ok {
				return // Channel closed
			}

			if _, err := w.Write(data); err != nil {
				log.Printf("Error writing to client %s: %v", client.ID, err)
				return
			}
			flusher.Flush()

		case <-r.Context().Done():
			return // Client disconnected

		case <-client.Done:
			return
		}
	}
}

// healthHandler provides a health check endpoint
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "healthy"}`))
}

// listStreamsHandler lists available streams
func listStreamsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	streams := make([]string, 0, len(streamConfigs))
	for key := range streamConfigs {
		streams = append(streams, "/stream/"+key)
	}

	response := fmt.Sprintf(`{"streams": ["%s"]}`, strings.Join(streams, `", "`))
	w.Write([]byte(response))
}

func main() {
	// Get configuration from environment or use defaults
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	audioDir := os.Getenv("AUDIO_DIR")
	if audioDir == "" {
		audioDir = "./audio"
	}

	// Verify audio directory exists
	if _, err := os.Stat(audioDir); os.IsNotExist(err) {
		log.Fatalf("Audio directory does not exist: %s", audioDir)
	}

	// Create stream manager
	streamManager := NewStreamManager(audioDir)

	// Setup HTTP router
	r := mux.NewRouter()

	// Stream endpoints
	r.HandleFunc("/stream/{category}", streamManager.streamHandler).Methods("GET", "OPTIONS")
	r.HandleFunc("/stream/{category}/{variation}", streamManager.streamHandler).Methods("GET", "OPTIONS")

	// Utility endpoints
	r.HandleFunc("/health", healthHandler).Methods("GET")
	r.HandleFunc("/streams", listStreamsHandler).Methods("GET")

	// Metrics endpoint
	r.Handle("/metrics", promhttp.Handler()).Methods("GET")

	// Middleware for metrics
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := mux.CurrentRoute(r)
			path, _ := route.GetPathTemplate()
			httpRequestsTotal.WithLabelValues(r.Method, path).Inc()
			next.ServeHTTP(w, r)
		})
	})

	// Create HTTP server
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // No timeout for streaming
		IdleTimeout:  60 * time.Second,
	}

	// Setup graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		log.Println("Shutting down server...")

		// Stop all streams
		streamManager.mu.Lock()
		for _, stream := range streamManager.streams {
			stream.Stop()
		}
		streamManager.mu.Unlock()

		// Shutdown HTTP server
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("Starting web radio server on port %s", port)
	log.Printf("Audio directory: %s", audioDir)
	log.Printf("Available streams:")
	for key := range streamConfigs {
		log.Printf("  - /stream/%s", key)
	}

	// Pre-start chanting streams
	log.Println("Pre-starting chanting streams...")
	chantingStreams := []string{"chanting/inside", "chanting/outside"}
	for _, streamKey := range chantingStreams {
		if _, err := streamManager.GetOrCreateStream(streamKey); err != nil {
			log.Printf("Warning: Failed to pre-start stream %s: %v", streamKey, err)
		} else {
			log.Printf("Pre-started stream: %s", streamKey)
		}
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}

	log.Println("Server stopped")
}
