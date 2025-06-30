package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A client listener is a channel that receives chunks of the stream.
type client chan []byte

// StreamBroadcaster manages a set of listeners for a single audio stream.
type StreamBroadcaster struct {
	mu        sync.Mutex
	listeners map[client]bool
}

// NewStreamBroadcaster creates a new broadcaster.
func NewStreamBroadcaster() *StreamBroadcaster {
	return &StreamBroadcaster{
		listeners: make(map[client]bool),
	}
}

// Broadcast sends a chunk of data to all active listeners.
func (b *StreamBroadcaster) Broadcast(chunk []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for listener := range b.listeners {
		select {
		case listener <- chunk:
		default:
			// If the listener's buffer is full, assume it's a slow client
			// and disconnect it to avoid blocking the entire broadcast.
			log.Println("Listener buffer full. Closing and removing listener.")
			delete(b.listeners, listener)
			close(listener)
		}
	}
}

// AddListener registers a new client and returns a channel for the stream.
func (b *StreamBroadcaster) AddListener() client {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Use a buffered channel to absorb some network latency.
	listener := make(client, 128)
	b.listeners[listener] = true
	log.Printf("New listener added. Total listeners: %d", len(b.listeners))
	return listener
}

// RemoveListener unregisters a client.
func (b *StreamBroadcaster) RemoveListener(listener client) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.listeners[listener]; ok {
		delete(b.listeners, listener)
		close(listener)
		log.Printf("Listener removed. Total listeners: %d", len(b.listeners))
	}
}

// Close terminates all listener channels.
func (b *StreamBroadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for listener := range b.listeners {
		delete(b.listeners, listener)
		close(listener)
	}
	log.Println("Broadcaster closed, all listeners disconnected.")
}

// broadcasterWriter is a helper type that implements io.Writer.
// It allows using a StreamBroadcaster as the destination for io.Copy.
type broadcasterWriter struct {
	b *StreamBroadcaster
}

func (bw *broadcasterWriter) Write(p []byte) (n int, err error) {
	// Create a copy of the slice, as the buffer `p` will be reused by io.Copy.
	chunk := make([]byte, len(p))
	copy(chunk, p)
	bw.b.Broadcast(chunk)
	return len(p), nil
}

// AudioConfig holds the configuration for a single audio stream type.
type AudioConfig struct {
	SourcePath  string
	FFmpegArgs  []string
	ContentType string
}

// StreamServer manages the audio configurations and active broadcasters.
type StreamServer struct {
	configs      map[string]AudioConfig
	broadcasters map[string]*StreamBroadcaster
	mu           sync.RWMutex
}

// NewStreamServer creates and initializes a new server instance.
func NewStreamServer() *StreamServer {
	return &StreamServer{
		configs:      make(map[string]AudioConfig),
		broadcasters: make(map[string]*StreamBroadcaster),
	}
}

// setupAudioConfigs defines the different audio streams and their FFmpeg settings.
func (s *StreamServer) setupAudioConfigs() {
	s.configs = map[string]AudioConfig{
		"chanting-inside": {
			SourcePath:  "./audio/chanting/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "", // Placeholder for the input file
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "128k",
				"-filter:a", "volume=0.8,lowpass=f=3000,highpass=f=300,acompressor=threshold=0.1:ratio=3:attack=20:release=0.1",
				"pipe:1", // Output to stdout
			},
		},
		"chanting-outside": {
			SourcePath:  "./audio/chanting/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "",
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "128k",
				"-filter:a", "volume=0.25,lowpass=f=1000,highpass=f=100,acompressor=threshold=0.1:ratio=2:attack=20:release=0.2",
				"pipe:1",
			},
		},
		"rain-inside": {
			SourcePath:  "./audio/rain/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "",
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "96k",
				"-filter:a", "volume=0.5,lowpass=f=2500,highpass=f=300,acompressor=threshold=0.2:ratio=1.5",
				"pipe:1",
			},
		},
		"rain-outside": {
			SourcePath:  "./audio/rain/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "",
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "96k",
				"-filter:a", "volume=0.6,acompressor=threshold=0.15:ratio=2",
				"pipe:1",
			},
		},
		"ambient": {
			SourcePath:  "./audio/ambient/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "",
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "64k",
				"-filter:a", "volume=0.3,lowpass=f=12000,highpass=f=40",
				"pipe:1",
			},
		},
		"loons": {
			SourcePath:  "./audio/ambient/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "",
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "64k",
				"-filter:a", "volume=0.15",
				"pipe:1",
			},
		},
		"crickets": {
			SourcePath:  "./audio/ambient/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "",
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "64k",
				"-filter:a", "volume=0.3",
				"pipe:1",
			},
		},
		"doves": {
			SourcePath:  "./audio/ambient/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "",
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "64k",
				"-filter:a", "volume=0.5",
				"pipe:1",
			},
		},
		"chickadees": {
			SourcePath:  "./audio/ambient/",
			ContentType: "audio/mpeg",
			FFmpegArgs: []string{
				"-re",
				"-stream_loop", "-1",
				"-i", "",
				"-f", "mp3",
				"-ar", "44100",
				"-ac", "2",
				"-ab", "64k",
				"-filter:a", "volume=0.4",
				"pipe:1",
			},
		},
	}
}

// getAudioFiles finds all audio files in a given directory, sorted alphabetically.
func (s *StreamServer) getAudioFiles(sourcePath string) ([]string, error) {
	var files []string
	// Try common audio formats
	formats := []string{"*.mp3", "*.wav", "*.ogg", "*.m4a", "*.flac"}
	for _, format := range formats {
		matches, err := filepath.Glob(filepath.Join(sourcePath, format))
		if err != nil {
			log.Printf("Error globbing for %s: %v", format, err)
			continue // Try next format
		}
		files = append(files, matches...)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no audio files found in %s", sourcePath)
	}

	sort.Strings(files) // Ensure consistent order
	return files, nil
}

// getRandomAudioFile finds an audio file in the given directory.
func (s *StreamServer) getRandomAudioFile(sourcePath string) (string, error) {
	files, err := s.getAudioFiles(sourcePath)
	if err != nil {
		return "", err
	}
	// For simplicity, return the first file. You could randomize this.
	return files[0], nil
}

// startAllStreams launches a manager goroutine for each configured audio stream.
func (s *StreamServer) startAllStreams() {
	for name, config := range s.configs {
		go s.manageStreamLifecycle(name, config)
	}
}

// manageStreamLifecycle ensures that a stream's FFmpeg process is always running.
// If the process exits, it will be restarted after a short delay.
func (s *StreamServer) manageStreamLifecycle(name string, config AudioConfig) {
	for {
		log.Printf("[%s] Starting stream process...", name)
		s.runAndBroadcastStream(name, config)
		log.Printf("[%s] Stream process ended. Restarting in 5 seconds...", name)
		time.Sleep(5 * time.Second)
	}
}

// runAndBroadcastStream starts an FFmpeg process and broadcasts its output.
func (s *StreamServer) runAndBroadcastStream(name string, config AudioConfig) {
	var cmd *exec.Cmd
	var audioFileDescription string

	args := make([]string, len(config.FFmpegArgs))
	copy(args, config.FFmpegArgs)

	// For chanting streams, we create a playlist of all files to play in order.
	// This allows for sequential playback of all files in the directory.
	if strings.HasPrefix(name, "chanting") {
		audioFiles, err := s.getAudioFiles(config.SourcePath)
		if err != nil {
			log.Printf("[%s] ERROR: Failed to get audio files: %v", name, err)
			return
		}
		if len(audioFiles) == 0 {
			log.Printf("[%s] ERROR: No audio files found in %s", name, config.SourcePath)
			return
		}

		// Create a temporary playlist file for FFmpeg's concat demuxer.
		playlistFile, err := os.CreateTemp("", "playlist-*.txt")
		if err != nil {
			log.Printf("[%s] ERROR: Failed to create playlist file: %v", name, err)
			return
		}
		defer os.Remove(playlistFile.Name()) // Clean up the temp file.

		for _, file := range audioFiles {
			absPath, err := filepath.Abs(file)
			if err != nil {
				log.Printf("[%s] WARNING: Could not get absolute path for '%s': %v", name, file, err)
				continue
			}
			// The 'file' directive is required by the concat demuxer.
			if _, err := playlistFile.WriteString(fmt.Sprintf("file '%s'\n", absPath)); err != nil {
				log.Printf("[%s] ERROR: Failed to write to playlist file: %v", name, err)
				playlistFile.Close()
				return
			}
		}
		playlistFile.Close() // Close the file so FFmpeg can read it.

		// Modify FFmpeg args to use the playlist instead of a single file.
		var newArgs []string
		for i := 0; i < len(args); i++ {
			if args[i] == "-i" && i+1 < len(args) && args[i+1] == "" {
				// Replace the single file input with the concat demuxer.
				newArgs = append(newArgs, "-f", "concat", "-safe", "0", "-i", playlistFile.Name())
				i++ // Also skip the placeholder "" argument.
			} else {
				newArgs = append(newArgs, args[i])
			}
		}
		args = newArgs
		audioFileDescription = fmt.Sprintf("playlist with %d files", len(audioFiles))
	} else {
		// For other streams, use the original logic of picking one file.
		audioFile, err := s.getRandomAudioFile(config.SourcePath)
		if err != nil {
			log.Printf("[%s] ERROR: Failed to get audio file: %v", name, err)
			return
		}
		for i := range args {
			if i > 0 && args[i-1] == "-i" && args[i] == "" {
				args[i] = audioFile
				break
			}
		}
		audioFileDescription = audioFile
	}

	cmd = exec.Command("ffmpeg", args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[%s] ERROR: Failed to create stdout pipe: %v", name, err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[%s] ERROR: Failed to start FFmpeg: %v", name, err)
		return
	}
	log.Printf("[%s] FFmpeg process started with: %s", name, audioFileDescription)

	broadcaster := NewStreamBroadcaster()
	s.mu.Lock()
	s.broadcasters[name] = broadcaster
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.broadcasters, name)
		s.mu.Unlock()
		broadcaster.Close()
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill()
		}
		if cmd != nil {
			cmd.Wait()
		}
		log.Printf("[%s] Cleaned up stream resources.", name)
	}()

	// This goroutine reads from FFmpeg and broadcasts the data.
	go func() {
		// Use io.Copy for efficient, buffered reading.
		_, err := io.Copy(&broadcasterWriter{b: broadcaster}, stdout)
		if err != nil {
			log.Printf("[%s] Broadcast source finished: %v", name, err)
		}
		// When Copy returns, the pipe is broken. Closing it will cause cmd.Wait() to unblock.
		stdout.Close()
	}()

	// Wait for the FFmpeg process to exit. This blocks until the stream ends.
	err = cmd.Wait()
	log.Printf("[%s] FFmpeg process exited with error: %v", name, err)
}

// handleStream connects a client to an existing stream broadcaster.
func (s *StreamServer) handleStream(streamType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers for all requests
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Range")

		// Handle preflight CORS requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		s.mu.RLock()
		broadcaster, broadcasterExists := s.broadcasters[streamType]
		config, configExists := s.configs[streamType]
		s.mu.RUnlock()

		if !broadcasterExists || !configExists {
			http.Error(w, "Stream not currently available", http.StatusNotFound)
			return
		}

		// Set headers for the audio stream
		w.Header().Set("Content-Type", config.ContentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		listener := broadcaster.AddListener()
		defer broadcaster.RemoveListener(listener)

		log.Printf("Client connected to stream %s", streamType)
		ctx := r.Context()

		for {
			select {
			case <-ctx.Done():
				log.Printf("Client disconnected from stream %s", streamType)
				return
			case chunk, ok := <-listener:
				if !ok {
					log.Printf("Stream %s was closed by the server.", streamType)
					return
				}
				if _, err := w.Write(chunk); err != nil {
					log.Printf("Error writing to client for stream %s: %v", streamType, err)
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
	}
}

// handleAmbientNature dynamically mixes and streams ambient sounds based on the time of day.
func (s *StreamServer) handleAmbientNature() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Range")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "audio/mpeg") // Assuming MP3 output
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		hour := time.Now().Hour()
		var audioFilesToMix []string
		basePath := "./audio/ambient/"

		// Determine which audio files to play based on the hour
		if hour < 6 { // 12am - 6am
			audioFilesToMix = []string{"loons.mp3", "crickets.mp3"}
		} else if hour < 12 { // 6am - 12pm
			audioFilesToMix = []string{"doves.mp3", "chickadees.mp3"}
		} else if hour < 18 { // 12pm - 6pm
			audioFilesToMix = []string{"doves.mp3", "chickadees.mp3"}
		} else { // 6pm - 12am
			audioFilesToMix = []string{"doves.mp3", "loons.mp3", "crickets.mp3"}
		}

		// Construct FFmpeg arguments for mixing
		var ffmpegArgs []string
		inputCount := len(audioFilesToMix)

		// Add inputs with stream_loop
		for _, file := range audioFilesToMix {
			ffmpegArgs = append(ffmpegArgs, "-stream_loop", "-1", "-i", filepath.Join(basePath, file))
		}

		// Build the filter_complex string
		if inputCount > 1 {
			filterComplex := ""
			for i := 0; i < inputCount; i++ {
				filterComplex += fmt.Sprintf("[%d:a]", i)
			}
			filterComplex += fmt.Sprintf("amerge=inputs=%d[aout]", inputCount)
			ffmpegArgs = append(ffmpegArgs, "-filter_complex", filterComplex, "-map", "[aout]")
		} else if inputCount == 1 {
			ffmpegArgs = append(ffmpegArgs, "-map", "0:a") // Map the single audio stream
		} else {
			// No audio files to mix, this case should ideally not happen based on the logic
			// but if it does, FFmpeg will likely error out.
		}

		// Common output arguments
		ffmpegArgs = append(ffmpegArgs,
			"-f", "mp3",
			"-ar", "44100",
			"-ac", "2",
			"-ab", "64k",
			"pipe:1", // Output to stdout
		)

		cmd := exec.Command("ffmpeg", ffmpegArgs...)
		cmd.Stderr = os.Stderr

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("ERROR: Failed to create stdout pipe for ambient nature stream: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if err := cmd.Start(); err != nil {
			log.Printf("ERROR: Failed to start FFmpeg for ambient nature stream: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		log.Printf("FFmpeg process started for ambient nature stream with args: %v", ffmpegArgs)

		// Ensure FFmpeg process is killed and resources are cleaned up when the client disconnects
		ctx := r.Context()
		go func() {
			<-ctx.Done()
			log.Println("Client disconnected from ambient nature stream. Killing FFmpeg process.")
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		}()

		// Stream FFmpeg output directly to the client
		_, err = io.Copy(w, stdout)
		if err != nil {
			log.Printf("Error streaming ambient nature audio to client: %v", err)
		}

		// Wait for the FFmpeg process to exit and clean up
		cmd.Wait()
		log.Println("FFmpeg process for ambient nature stream exited.")
	}
}

// healthCheck provides a simple endpoint to verify the server is running.
func (s *StreamServer) healthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "healthy", "timestamp": "` + time.Now().Format(time.RFC3339) + `"}`))
}

func main() {
	// Check if FFmpeg is available on the system
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Fatal("FATAL: FFmpeg not found in PATH. Please install FFmpeg.")
	}

	server := NewStreamServer()
	server.setupAudioConfigs()

	// Start the persistent FFmpeg processes in the background
	server.startAllStreams()

	// Setup HTTP routes
	http.HandleFunc("/stream/chanting/inside", server.handleStream("chanting-inside"))
	http.HandleFunc("/stream/chanting/outside", server.handleStream("chanting-outside"))
	http.HandleFunc("/stream/rain/inside", server.handleStream("rain-inside"))
	http.HandleFunc("/stream/rain/outside", server.handleStream("rain-outside"))
	http.HandleFunc("/stream/ambient-nature", server.handleAmbientNature())
	http.HandleFunc("/health", server.healthCheck)

	// Serve static files (if any)
	http.Handle("/", http.FileServer(http.Dir("./static/")))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Byzantine Chanting Radio Server starting on port %s", port)
	log.Printf("Streaming endpoints are being initialized...")
	log.Printf("  - /stream/chanting/inside")
	log.Printf("  - /stream/chanting/outside")
	log.Printf("  - /stream/rain/inside")
	log.Printf("  - /stream/rain/outside")
	log.Printf("  - /stream/ambient")
	log.Printf("  - /stream/ambient-nature")
	log.Printf("  - /health")

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
