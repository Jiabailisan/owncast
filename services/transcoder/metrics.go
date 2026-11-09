package transcoder

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// clockTicks is the Linux USER_HZ used to convert /proc/<pid>/stat CPU jiffies
// to seconds. It is 100 on essentially every Linux configuration Owncast runs
// on (including the NAS); the sampler degrades to N/A if /proc is unavailable.
const clockTicks = 100.0

// processMetrics holds the live metrics for one ffmpeg process. The progress
// scanner, the /proc sampler and the GPU sampler write it concurrently while
// the admin FFmpeg panel reads snapshots.
type processMetrics struct {
	mu sync.RWMutex

	speed          *float64
	cpuPercent     *float64
	rssBytes       *int64
	gpuPercent     *float64
	gpuMemoryBytes *int64
	gpuName        string

	hasCPUSample bool
	lastCPUTime  time.Time
	lastCPUTicks uint64
}

// processMetricsSnapshot is a copy of the current metric values.
type processMetricsSnapshot struct {
	Speed          *float64
	CPUPercent     *float64
	RSSBytes       *int64
	GPUPercent     *float64
	GPUMemoryBytes *int64
	GPUName        string
}

// snapshot returns a copy of the current metrics. Nil means N/A.
func (m *processMetrics) snapshot() processMetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return processMetricsSnapshot{
		Speed:          copyFloat64(m.speed),
		CPUPercent:     copyFloat64(m.cpuPercent),
		RSSBytes:       copyInt64(m.rssBytes),
		GPUPercent:     copyFloat64(m.gpuPercent),
		GPUMemoryBytes: copyInt64(m.gpuMemoryBytes),
		GPUName:        m.gpuName,
	}
}

// reset clears all values so a stopped process reports N/A.
func (m *processMetrics) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.speed = nil
	m.cpuPercent = nil
	m.rssBytes = nil
	m.gpuPercent = nil
	m.gpuMemoryBytes = nil
	m.gpuName = ""
	m.hasCPUSample = false
	m.lastCPUTicks = 0
}

func copyFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// consumeProgress parses ffmpeg -progress key=value output until the stream
// closes. ffmpeg emits one block per update interval on stdout.
func (m *processMetrics) consumeProgress(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		m.handleProgressLine(scanner.Text())
	}
}

// handleProgressLine stores the values the FFmpeg panel needs from one
// -progress line. Unknown keys are ignored; "speed=N/A" (the startup state
// before any frame) leaves the value nil.
func (m *processMetrics) handleProgressLine(line string) {
	key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return
	}
	if strings.TrimSpace(key) != "speed" {
		return
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(value), "x")
	speed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || speed <= 0 {
		return
	}
	m.mu.Lock()
	m.speed = &speed
	m.mu.Unlock()
}

// processSampler samples CPU/RSS (and GPU when an NVENC encoder is active)
// while the ffmpeg process runs. Stop waits for the sampler goroutine.
type processSampler struct {
	stop chan struct{}
	done sync.WaitGroup
}

// startProcessSampler starts the CPU/RSS/GPU sampler for a just-started ffmpeg
// child process. The encoder name decides whether GPU sampling is attempted:
// NVENC uses nvidia-smi; other hardware encoders have no portable in-process
// query and stay N/A.
func (t *Transcoder) startProcessSampler(pid int) *processSampler {
	sampler := &processSampler{stop: make(chan struct{})}
	encoderName := ""
	if t.codec != nil {
		encoderName = t.codec.Name()
	}
	metrics := &t.state.metrics
	sampler.done.Add(1)
	go func() {
		defer sampler.done.Done()
		sampler.run(pid, encoderName, metrics)
	}()
	return sampler
}

// Stop terminates sampling and waits for the goroutine to exit.
func (s *processSampler) Stop() {
	close(s.stop)
	s.done.Wait()
}

// run is the sampling loop: CPU/RSS every second, GPU every second tick when
// the encoder is NVENC and nvidia-smi is available.
func (s *processSampler) run(pid int, encoderName string, metrics *processMetrics) {
	useGPU := isNvidiaSMIEncoder(encoderName)
	if useGPU {
		if _, err := exec.LookPath(nvidiaSMICommand); err != nil {
			useGPU = false
		}
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	tick := 0
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.sampleCPU(pid, metrics)
			tick++
			if useGPU && tick%2 == 0 {
				s.sampleGPU(pid, metrics)
			}
		}
	}
}

// sampleCPU reads /proc/<pid> CPU ticks and RSS and derives CPUPercent from the
// delta between samples (100 = one full CPU core). The first sample only
// records the baseline; values stay N/A until the second sample.
func (s *processSampler) sampleCPU(pid int, metrics *processMetrics) {
	ticks, rssBytes, ok := readProcessStats(pid)
	if !ok {
		return
	}
	now := time.Now()

	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	if metrics.hasCPUSample {
		elapsed := now.Sub(metrics.lastCPUTime).Seconds()
		if elapsed > 0 && ticks >= metrics.lastCPUTicks {
			deltaTicks := float64(ticks - metrics.lastCPUTicks)
			percent := deltaTicks / clockTicks / elapsed * 100
			metrics.cpuPercent = &percent
		}
	}
	metrics.lastCPUTime = now
	metrics.lastCPUTicks = ticks
	metrics.hasCPUSample = true
	if rssBytes > 0 {
		metrics.rssBytes = &rssBytes
	}
}

// sampleGPU runs one best-effort nvidia-smi poll: global utilization plus the
// FFmpeg child's per-process memory from the compute-apps query (matched by
// PID). Missing nvidia-smi/GPU or a failed query leaves the values N/A.
func (s *processSampler) sampleGPU(pid int, metrics *processMetrics) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, nvidiaSMICommand,
		"--query-gpu=name,utilization.gpu,memory.used",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return
	}
	name, utilization, globalMemoryBytes, ok := parseNvidiaGPUStats(string(output))
	if !ok {
		return
	}

	// Per-process GPU memory (MiB) for our exact ffmpeg child, when the driver
	// reports compute apps. The global memory.used is the fallback context.
	if memoryBytes, found := nvidiaSMIComputeAppMemory(ctx, pid); found {
		_ = globalMemoryBytes
		globalMemoryBytes = memoryBytes
	}

	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.gpuPercent = &utilization
	metrics.gpuMemoryBytes = &globalMemoryBytes
	metrics.gpuName = name
}

// nvidiaSMIComputeAppMemory returns the used GPU memory (bytes) of the compute
// process with the given PID.
func nvidiaSMIComputeAppMemory(ctx context.Context, pid int) (int64, bool) {
	output, err := exec.CommandContext(ctx, nvidiaSMICommand,
		"--query-compute-apps=pid,used_memory",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, false
	}
	return parseNvidiaComputeApps(string(output), pid)
}

const nvidiaSMICommand = "nvidia-smi"

// isNvidiaSMIEncoder reports whether the configured encoder is an NVIDIA NVENC
// encoder, the only family nvidia-smi can report utilization for.
func isNvidiaSMIEncoder(encoderName string) bool {
	name := strings.ToLower(strings.TrimSpace(encoderName))
	return strings.Contains(name, "nvenc")
}

// parseNvidiaGPUStats parses one nvidia-smi --query-gpu output line:
// "Quadro P600, 24, 401" (name, utilization.gpu %, memory.used MiB).
func parseNvidiaGPUStats(output string) (string, float64, int64, bool) {
	line := firstNonEmptyLine(output)
	if line == "" {
		return "", 0, 0, false
	}
	parts := strings.SplitN(line, ",", 3)
	if len(parts) != 3 {
		return "", 0, 0, false
	}
	name := strings.TrimSpace(parts[0])
	utilization, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return "", 0, 0, false
	}
	memoryMiB, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil {
		return "", 0, 0, false
	}
	memoryBytes := int64(memoryMiB * 1024 * 1024)
	return name, utilization, memoryBytes, true
}

// parseNvidiaComputeApps parses nvidia-smi --query-compute-apps=pid,used_memory
// output lines ("22517, 398", used_memory in MiB) and returns the memory of the
// matching PID.
func parseNvidiaComputeApps(output string, pid int) (int64, bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}
		linePID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || linePID != pid {
			continue
		}
		memoryMiB, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			continue
		}
		return int64(memoryMiB * 1024 * 1024), true
	}
	return 0, false
}

// firstNonEmptyLine returns the first non-empty trimmed line of output.
func firstNonEmptyLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
