/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package benchmark

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

// Config controls the benchmark execution.
type Config struct {
	Workers        int           // Number of concurrent goroutines
	Duration       time.Duration // Time to record execution
	WarmupDuration time.Duration // Time to run before recording
	RateLimit      float64       // Total Ops/Sec limit (0 = Unlimited/Closed-Loop)
}

func NewConfig(workers int, duration time.Duration, warmupDuration time.Duration) Config {
	return Config{Workers: workers, Duration: duration, WarmupDuration: warmupDuration}
}

// Result holds the comprehensive benchmark metrics.
type Result struct {
	Config     Config
	GoRoutines int // Workers (kept for compatibility)
	// GoRoutinesCreated is the net number of goroutines observed above the
	// baseline during the recording window. It reflects scheduler pressure
	// caused by the executor strategy (serial≈0, unbounded=n, pool=fixed).
	GoRoutinesCreated int64

	// Throughput
	OpsTotal      uint64
	Duration      time.Duration
	OpsPerSecReal float64 // Wall-clock throughput
	OpsPerSecPure float64 // Theoretical concurrency / avg_latency

	// Latency Stats
	AvgLatency    time.Duration
	StdDevLatency time.Duration
	Variance      float64       // Variance in nanoseconds^2
	P50Latency    time.Duration // Median
	P75Latency    time.Duration
	P95Latency    time.Duration
	P99Latency    time.Duration
	P999Latency   time.Duration // 99.9th percentile
	P9999Latency  time.Duration // 99.99th percentile
	MinLatency    time.Duration
	MaxLatency    time.Duration
	IQR           time.Duration // Interquartile Range (P75 - P25)
	Jitter        time.Duration // Avg change between consecutive latencies
	CoeffVar      float64       // Coefficient of Variation (StdDev / Mean)

	// Stability & Time Series
	Timeline []TimePoint

	// Memory & GC Stats
	BytesPerOp    uint64
	AllocsPerOp   uint64
	AllocRateMBPS float64       // Allocations in MB per second
	NumGC         uint32        // Number of GC cycles during the recorded phase
	GCPauseTotal  time.Duration // Total time the world was stopped for GC
	GCOverhead    float64       // Percentage of time spent in GC

	// Reliability
	ErrorCount uint64
	ErrorRate  float64
	Histogram  []Bucket
}

// TimePoint captures system state at a specific moment.
type TimePoint struct {
	Timestamp   time.Duration
	OpsSec      float64
	ActiveCount int
}

// Bucket represents a latency range and its frequency.
type Bucket struct {
	LowBound  time.Duration
	HighBound time.Duration
	Count     int
}

// chunk holds a fixed-size batch of latencies.
const chunkSize = 10000

type chunk struct {
	data [chunkSize]time.Duration
	next *chunk
	idx  int
}

// workerStats aggregates results from a single worker.
type workerStats struct {
	head   *chunk
	errors uint64
}

// ANSI Color Codes for output.
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
)

// runState holds the concurrency primitives and shared mutable state coordinating
// RunBenchmark's worker goroutines and its timeline monitor goroutine. It must only
// ever be accessed through a single shared *runState (never copied), since it embeds
// sync.WaitGroup and atomic values.
//
// startGlobal is written once by RunBenchmark's own goroutine before recording is
// flipped to true via the recording atomic; workers only ever read startGlobal after
// observing recording.Load() == true, so the atomic Store/Load pair establishes the
// happens-before edge that makes that unsynchronized read of startGlobal safe. Keep
// that ordering intact if this is touched again.
type runState struct {
	running        atomic.Bool
	recording      atomic.Bool
	opsCounter     atomic.Uint64
	startWg        sync.WaitGroup
	endWg          sync.WaitGroup
	startGlobal    time.Time
	peakGoroutines atomic.Int64
}

// applyDefaults fills in sane defaults for unset Config fields.
func applyDefaults(cfg *Config) {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 1 * time.Second
	}
}

// computeRateLimitInterval returns the per-worker sleep interval that together
// achieves cfg.RateLimit total ops/sec, or 0 for closed-loop (unlimited) execution.
func computeRateLimitInterval(cfg Config) time.Duration {
	if cfg.RateLimit <= 0 {
		return 0
	}
	ratePerWorker := cfg.RateLimit / float64(cfg.Workers)
	if ratePerWorker <= 0 {
		return 0
	}

	return time.Duration(float64(time.Second) / ratePerWorker)
}

// RunBenchmark executes the benchmark.
func RunBenchmark[T any](
	cfg Config,
	setup func() T,
	work func(T) error,
) Result {
	applyDefaults(&cfg)

	// ---------------------------------------------------------
	// PHASE 1: Memory Analysis (Serial & Isolated)
	// ---------------------------------------------------------
	memBytes, memAllocs := measureMemory(setup, work)

	// ---------------------------------------------------------
	// PHASE 2: Throughput & Latency (Concurrent)
	// ---------------------------------------------------------
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	st := &runState{}
	st.running.Store(true)
	st.recording.Store(false)
	workerResults := make([]workerStats, cfg.Workers)

	intervalPerOp := computeRateLimitInterval(cfg)

	st.startWg.Add(cfg.Workers)
	st.endWg.Add(cfg.Workers)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := range cfg.Workers {
		workerID := i
		go func() {
			defer st.endWg.Done()
			runWorker(workerID, setup, work, intervalPerOp, st, workerResults)
		}()
	}

	st.startWg.Wait()

	if cfg.WarmupDuration > 0 {
		time.Sleep(cfg.WarmupDuration)
	}

	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	st.startGlobal = time.Now()
	st.recording.Store(true)

	// Timeline Monitor
	timeline := make([]TimePoint, 0, int(cfg.Duration.Seconds())+1)

	var monitorWg sync.WaitGroup
	monitorWg.Add(1)

	// Baseline goroutine count: workers + monitor + main + any framework goroutines.
	// Captured just before recording so executor goroutines are not yet active.
	goroutineBaseline := int64(runtime.NumGoroutine())
	st.peakGoroutines.Store(goroutineBaseline)

	go func() {
		defer monitorWg.Done()
		monitorTimeline(ctx, st, &timeline)
	}()

	time.Sleep(cfg.Duration)

	st.running.Store(false)
	st.endWg.Wait()
	cancel()

	globalDuration := time.Since(st.startGlobal)
	monitorWg.Wait() // BLOCK here until monitor goroutine returns

	runtime.ReadMemStats(&memAfter)

	goroutinesCreated := max(st.peakGoroutines.Load()-goroutineBaseline, 0)

	return analyzeResults(cfg, workerResults, memBytes, memAllocs, memBefore, memAfter, globalDuration, timeline, goroutinesCreated)
}

// runWorker is one benchmark worker's full lifecycle: it waits at the start barrier,
// then repeatedly calls work(d) until st.running goes false, recording latencies (via
// recordLatency) for operations that both start after recording began and observe
// st.recording == true. Its result is stored into results[workerID].
func runWorker[T any](workerID int, setup func() T, work func(T) error, intervalPerOp time.Duration, st *runState, results []workerStats) {
	currentChunk := &chunk{}
	headChunk := currentChunk
	var localErrors uint64
	var nextTick time.Time

	if intervalPerOp > 0 {
		nextTick = time.Now()
	}

	// This acts as a "starting gun" (Barrier pattern).
	// It ensures that all worker goroutines are spawned, initialized, and ready to go before any of them begin execution.
	st.startWg.Done()
	st.startWg.Wait()

	d := setup()

	for st.running.Load() {
		nextTick = applyThrottle(nextTick, intervalPerOp)

		tStart := time.Now()
		err := work(d)
		dur := time.Since(tStart)

		if st.recording.Load() {
			// STRICT CHECK: Ensure op started AFTER recording began
			if tStart.After(st.startGlobal) {
				st.opsCounter.Add(1)
				if err != nil {
					localErrors++
				}
				currentChunk = recordLatency(currentChunk, dur)
			}
		}
	}
	results[workerID] = workerStats{head: headChunk, errors: localErrors}
}

// applyThrottle implements open-loop throttling: it sleeps until nextTick if needed,
// then returns the following tick. intervalPerOp <= 0 means closed-loop (no throttle).
func applyThrottle(nextTick time.Time, intervalPerOp time.Duration) time.Time {
	if intervalPerOp <= 0 {
		return nextTick
	}

	now := time.Now()
	if now.Before(nextTick) {
		time.Sleep(nextTick.Sub(now))
	}
	nextTick = nextTick.Add(intervalPerOp)
	if time.Since(nextTick) > intervalPerOp*10 {
		nextTick = time.Now()
	}

	return nextTick
}

// recordLatency appends dur onto currentChunk, allocating and linking a new chunk
// first if the current one is full, and returns the (possibly new) current chunk.
func recordLatency(currentChunk *chunk, dur time.Duration) *chunk {
	if currentChunk.idx >= chunkSize {
		newC := &chunk{}
		currentChunk.next = newC
		currentChunk = newC
	}
	currentChunk.data[currentChunk.idx] = dur
	currentChunk.idx++

	return currentChunk
}

// monitorTimeline samples throughput and peak goroutine count once per second,
// appending TimePoints to *timeline, until ctx is cancelled or st.running goes false.
func monitorTimeline(ctx context.Context, st *runState, timeline *[]TimePoint) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	var prevOps uint64
	startTime := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if !st.running.Load() {
				return
			}
			currOps := st.opsCounter.Load()
			delta := currOps - prevOps
			prevOps = currOps
			pt := TimePoint{Timestamp: t.Sub(startTime), OpsSec: float64(delta)}
			*timeline = append(*timeline, pt)

			updatePeakGoroutines(st)
		}
	}
}

// updatePeakGoroutines updates st.peakGoroutines to the current goroutine count if it
// is higher than the previously recorded peak.
//
// Track peak goroutine count during recording. Unbounded executors spawn goroutines
// per proof that live microseconds — we sample every tick to catch their peak.
func updatePeakGoroutines(st *runState) {
	current := int64(runtime.NumGoroutine())
	for {
		old := st.peakGoroutines.Load()
		if current <= old {
			break
		}
		if st.peakGoroutines.CompareAndSwap(old, current) {
			break
		}
	}
}

func measureMemory[T any](setup func() T, work func(T) error) (bytes, allocs uint64) {
	var totalAllocs, totalBytes uint64
	const samples = 5
	data := setup()

	for range samples {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
		var m1, m2 runtime.MemStats
		runtime.ReadMemStats(&m1)
		_ = work(data)
		runtime.ReadMemStats(&m2)
		totalAllocs += m2.Mallocs - m1.Mallocs
		totalBytes += m2.TotalAlloc - m1.TotalAlloc
	}

	return totalBytes / samples, totalAllocs / samples
}

// latencyStats holds the derived throughput/latency/distribution metrics computed
// from a sorted slice of recorded per-op latencies.
type latencyStats struct {
	opsPerSecReal                   float64
	opsPerSecPure                   float64
	avgLatency                      time.Duration
	stdDev                          time.Duration
	variance                        float64
	p50, p75, p95, p99, p999, p9999 time.Duration
	minLat, maxLat                  time.Duration
	iqr                             time.Duration
	coeffVar                        float64
}

// gcStats holds the GC/memory-pressure metrics observed between two MemStats
// snapshots taken before and after the recording window.
type gcStats struct {
	numGC      uint32
	pauseTotal time.Duration
	gcOverhead float64
	allocRate  float64
}

// chunkAggregate holds one worker's contribution to the aggregated latency totals.
type chunkAggregate struct {
	totalOps      uint64
	totalTimeNs   int64
	jitterSum     float64
	jitterSamples uint64
}

// aggregateWorkerChunks walks one worker's linked list of latency chunks, appending
// every recorded (non-zero) latency onto allLatencies and returning that worker's op
// count, total latency time, and jitter sum/count alongside the extended slice.
func aggregateWorkerChunks(head *chunk, allLatencies []time.Duration) (chunkAggregate, []time.Duration) {
	var agg chunkAggregate
	curr := head
	var prevLat time.Duration
	first := true

	for curr != nil {
		limit := curr.idx
		agg.totalOps += uint64(limit) // #nosec G115
		for k := range limit {
			lat := curr.data[k]
			if lat == 0 {
				continue
			}

			agg.totalTimeNs += int64(lat)
			allLatencies = append(allLatencies, lat)

			if !first {
				diff := float64(lat - prevLat)
				if diff < 0 {
					diff = -diff
				}
				agg.jitterSum += diff
				agg.jitterSamples++
			}
			prevLat = lat
			first = false
		}
		curr = curr.next
	}

	return agg, allLatencies
}

// aggregateWorkerLatencies flattens every worker's chunked latency samples into a
// single slice, alongside the running totals (ops, errors, total latency time) and
// the average absolute jitter between consecutive recorded latencies within a worker.
func aggregateWorkerLatencies(workers []workerStats, estimatedOps uint64) (totalOps, totalErrors uint64, totalTimeNs int64, allLatencies []time.Duration, jitter time.Duration) {
	allLatencies = make([]time.Duration, 0, estimatedOps)

	var totalJitter float64
	var jitterSamples uint64

	for _, w := range workers {
		totalErrors += w.errors

		var agg chunkAggregate
		agg, allLatencies = aggregateWorkerChunks(w.head, allLatencies)
		totalOps += agg.totalOps
		totalTimeNs += agg.totalTimeNs
		totalJitter += agg.jitterSum
		jitterSamples += agg.jitterSamples
	}

	if jitterSamples > 0 {
		jitter = time.Duration(totalJitter / float64(jitterSamples))
	}

	return totalOps, totalErrors, totalTimeNs, allLatencies, jitter
}

// computeLatencyStats sorts allLatencies in place and derives throughput, percentile,
// spread and variability metrics from it.
func computeLatencyStats(cfg Config, allLatencies []time.Duration, totalOps uint64, totalTimeNs int64, duration time.Duration) latencyStats {
	opsPerSecReal := float64(totalOps) / duration.Seconds()
	avgLatency := time.Duration(totalTimeNs / int64(totalOps)) // #nosec G115
	opsPerSecPure := 0.0
	if avgLatency > 0 {
		opsPerSecPure = float64(cfg.Workers) / avgLatency.Seconds()
	}

	// Optimization: Use slices.Sort
	slices.Sort(allLatencies)

	// Percentiles
	p25 := percentile(allLatencies, 0.25)
	p50 := percentile(allLatencies, 0.50)
	p75 := percentile(allLatencies, 0.75)
	p95 := percentile(allLatencies, 0.95)
	p99 := percentile(allLatencies, 0.99)
	p999 := percentile(allLatencies, 0.999)
	p9999 := percentile(allLatencies, 0.9999)
	minLat := allLatencies[0]
	maxLat := allLatencies[len(allLatencies)-1]

	// Stats
	iqr := p75 - p25

	meanNs := float64(avgLatency.Nanoseconds())
	var sumSqDiff float64
	for _, lat := range allLatencies {
		diff := float64(lat.Nanoseconds()) - meanNs
		sumSqDiff += diff * diff
	}
	variance := sumSqDiff / float64(len(allLatencies))
	stdDev := time.Duration(math.Sqrt(variance))

	coeffVar := 0.0
	if avgLatency > 0 {
		coeffVar = float64(stdDev) / float64(avgLatency)
	}

	return latencyStats{
		opsPerSecReal: opsPerSecReal,
		opsPerSecPure: opsPerSecPure,
		avgLatency:    avgLatency,
		stdDev:        stdDev,
		variance:      variance,
		p50:           p50,
		p75:           p75,
		p95:           p95,
		p99:           p99,
		p999:          p999,
		p9999:         p9999,
		minLat:        minLat,
		maxLat:        maxLat,
		iqr:           iqr,
		coeffVar:      coeffVar,
	}
}

// computeGCStats derives GC pause/overhead and allocation-rate metrics from the
// MemStats snapshots taken immediately before and after the recording window.
func computeGCStats(mStart, mEnd runtime.MemStats, duration time.Duration) gcStats {
	numGC := mEnd.NumGC - mStart.NumGC
	pauseNs := mEnd.PauseTotalNs - mStart.PauseTotalNs
	gcOverhead := (float64(pauseNs) / float64(duration.Nanoseconds())) * 100
	allocRate := (float64(mEnd.TotalAlloc-mStart.TotalAlloc) / 1024 / 1024) / duration.Seconds()

	return gcStats{
		numGC:      numGC,
		pauseTotal: time.Duration(pauseNs), // #nosec G115
		gcOverhead: gcOverhead,
		allocRate:  allocRate,
	}
}

func analyzeResults(
	cfg Config,
	workers []workerStats,
	memBytes, memAllocs uint64,
	mStart, mEnd runtime.MemStats,
	duration time.Duration,
	timeline []TimePoint,
	goroutinesCreated int64,
) Result {
	estimatedOps := uint64(len(workers)) * uint64(chunkSize) * 2
	totalOps, totalErrors, totalTimeNs, allLatencies, jitter := aggregateWorkerLatencies(workers, estimatedOps)

	if totalOps == 0 {
		return Result{Config: cfg, ErrorRate: 100.0}
	}

	ls := computeLatencyStats(cfg, allLatencies, totalOps, totalTimeNs, duration)
	gc := computeGCStats(mStart, mEnd, duration)

	return Result{
		Config:            cfg,
		GoRoutines:        cfg.Workers,
		OpsTotal:          totalOps,
		Duration:          duration,
		OpsPerSecReal:     ls.opsPerSecReal,
		OpsPerSecPure:     ls.opsPerSecPure,
		AvgLatency:        ls.avgLatency,
		StdDevLatency:     ls.stdDev,
		Variance:          ls.variance,
		P50Latency:        ls.p50,
		P75Latency:        ls.p75,
		P95Latency:        ls.p95,
		P99Latency:        ls.p99,
		P999Latency:       ls.p999,
		P9999Latency:      ls.p9999,
		MinLatency:        ls.minLat,
		MaxLatency:        ls.maxLat,
		IQR:               ls.iqr,
		Jitter:            jitter,
		CoeffVar:          ls.coeffVar,
		BytesPerOp:        memBytes,
		AllocsPerOp:       memAllocs,
		AllocRateMBPS:     gc.allocRate,
		NumGC:             gc.numGC,
		GCPauseTotal:      gc.pauseTotal,
		GCOverhead:        gc.gcOverhead,
		ErrorCount:        totalErrors,
		ErrorRate:         (float64(totalErrors) / float64(totalOps)) * 100,
		Histogram:         calcHistogramImproved(allLatencies, ls.minLat, ls.maxLat, 20),
		Timeline:          timeline,
		GoRoutinesCreated: goroutinesCreated,
	}
}

// -----------------------------------------------------------------------------
// OUTPUT FORMATTING (Restored Original Logic)
// -----------------------------------------------------------------------------

func (r Result) Print() {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print Header info
	if r.Config.RateLimit > 0 {
		writef(w, "%s[Running in Open-Loop Mode (Limit: %.0f/s)]%s\n", ColorCyan, r.Config.RateLimit, ColorReset)
	}

	cvPct, tailRatio := r.printMainMetrics(w)
	r.printSystemHealth(w)
	r.printHeatmap(w)
	r.printAnalysis(w, cvPct, tailRatio)

	// Append the new Sparkline (Timeline) at the very bottom
	if len(r.Timeline) > 1 {
		writeLine(w, "")
		writeLine(w, ColorBlue+"--- Throughput Timeline ---"+ColorReset)
		printSparkline(w, r.Timeline)
		writeLine(w, "")
	}

	if err := w.Flush(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "benchmark: flush error:", err)
	}
}

// printMainMetrics prints the main metrics, latency distribution and stability
func (r Result) printMainMetrics(w *tabwriter.Writer) (cvPct float64, tailRatio float64) {
	// Helper for coloring status.
	status := func(condition bool, goodMsg, badMsg string) string {
		if condition {
			return ColorGreen + goodMsg + ColorReset
		}

		return ColorRed + badMsg + ColorReset
	}

	writeLine(w, "Metric\tValue\tDescription")
	writeLine(w, "------\t-----\t-----------")
	writef(w, "Workers\t%d\t\n", r.GoRoutines)
	writef(w, "Total Ops\t%d\t%s\n",
		r.OpsTotal,
		status(r.OpsTotal > 5000, "(Robust Sample)", "(Low Sample Size)"),
	)
	writef(w, "Duration\t%v\t%s\n",
		r.Duration.Round(time.Millisecond),
		status(r.Duration > 1*time.Second, "(Good Duration)", "(Too Short < 1s)"),
	)
	writef(w, "Real Throughput\t%.2f/s\tObserved Ops/sec (Wall Clock)\n", r.OpsPerSecReal)

	// Overhead Check.
	overheadPct := 0.0
	if r.OpsPerSecPure > 0 && r.OpsPerSecReal > 0 {
		overheadPct = (1.0 - (r.OpsPerSecReal / r.OpsPerSecPure)) * 100
	}
	overheadStatus := "(Low Overhead)"
	if overheadPct > 15.0 {
		overheadStatus = ColorYellow + fmt.Sprintf("(High Setup Cost: %.1f%%)", overheadPct) + ColorReset
	}
	writef(w, "Pure Throughput\t%.2f/s\tTheoretical Max %s\n", r.OpsPerSecPure, overheadStatus)

	writeLine(w, "")
	writeLine(w, "Latency Distribution:")
	writef(w, " Min\t%v\t\n", r.MinLatency)
	writef(w, " P50 (Median)\t%v\t\n", r.P50Latency)
	writef(w, " Average\t%v\t\n", r.AvgLatency)
	writef(w, " P95\t%v\t\n", r.P95Latency)
	writef(w, " P99\t%v\t\n", r.P99Latency)

	// Add new high-precision metrics seamlessly
	writef(w, " P99.9\t%v\t\n", r.P999Latency)

	// Tail Latency Check.
	tailRatio = 0.0
	if r.P99Latency > 0 {
		tailRatio = float64(r.MaxLatency) / float64(r.P99Latency)
	}
	maxStatus := ColorGreen + "(Stable Tail)" + ColorReset
	if tailRatio > 10.0 {
		maxStatus = ColorRed + fmt.Sprintf("(Extreme Outliers: Max is %.1fx P99)", tailRatio) + ColorReset
	}
	writef(w, " Max\t%v\t%s\n", r.MaxLatency, maxStatus)

	writeLine(w, "")
	writeLine(w, "Stability Metrics:")
	writef(w, " Std Dev\t%v\t\n", r.StdDevLatency)
	writef(w, " IQR\t%v\tInterquartile Range\n", r.IQR)
	writef(w, " Jitter\t%v\tAvg delta per worker\n", r.Jitter)

	// CV Check.
	cvPct = r.CoeffVar * 100
	cvStatus := ColorGreen + "Excellent Stability (<5%)" + ColorReset
	if cvPct > 20.0 {
		cvStatus = ColorRed + "Unstable (>20%) - Result is Noisy" + ColorReset
	} else if cvPct > 10.0 {
		cvStatus = ColorYellow + "Moderate Variance (10-20%)" + ColorReset
	} else if cvPct > 5.0 {
		cvStatus = "(Acceptable 5-10%)"
	}
	writef(w, " CV\t%.2f%%\t%s\n", cvPct, cvStatus)
	writeLine(w, "")

	return cvPct, tailRatio
}

// printSystemHealth renders GC, Memory and Error statistics
func (r Result) printSystemHealth(w *tabwriter.Writer) {
	writeLine(w, "System Health & Reliability:")

	// 1. Error Rate
	errStatus := ColorGreen + "(100% Success)" + ColorReset
	if r.ErrorRate > 0 {
		errStatus = ColorRed + fmt.Sprintf("(%.2f%% Failures)", r.ErrorRate) + ColorReset
	}
	writef(w, " Error Rate\t%.4f%%\t%s (%d errors)\n", r.ErrorRate, errStatus, r.ErrorCount)

	// 2. Memory Allocations
	writef(w, " Memory\t%d B/op\tAllocated bytes per operation\n", r.BytesPerOp)
	writef(w, " Allocs\t%d allocs/op\tAllocations per operation\n", r.AllocsPerOp)
	writef(w, " Alloc Rate\t%.2f MB/s\tMemory pressure on system\n", r.AllocRateMBPS)

	// 3. GC Analysis
	gcStatus := ColorGreen + "(Healthy)" + ColorReset
	if r.GCOverhead > 5.0 {
		gcStatus = ColorRed + "(Severe GC Thrashing)" + ColorReset
	} else if r.GCOverhead > 1.0 {
		gcStatus = ColorYellow + "(High GC Pressure)" + ColorReset
	}
	writef(w, " GC Overhead\t%.2f%%\t%s\n", r.GCOverhead, gcStatus)
	writef(w, " GC Pause\t%v\tTotal Stop-The-World time\n", r.GCPauseTotal)
	writef(w, " GC Cycles\t%d\tFull garbage collection cycles\n", r.NumGC)
	writef(w, " Goroutines Created\t%d\tNet goroutines above baseline during recording\n", r.GoRoutinesCreated)
	writeLine(w, "")
}

// printHeatmap renders the histogram heatmap.
func (r Result) printHeatmap(w *tabwriter.Writer) {
	writeLine(w, "Latency Heatmap (Dynamic Range):")
	writeLine(w, "Range\tFreq\tDistribution Graph")

	maxCount := heatmapMaxCount(r.Histogram)

	for _, b := range r.Histogram {
		if b.Count == 0 {
			continue
		}
		printHeatmapBucket(w, b, maxCount, r.OpsTotal)
	}
}

// heatmapMaxCount returns the highest bucket count, used to scale bar lengths.
func heatmapMaxCount(histogram []Bucket) int {
	maxCount := 0
	for _, b := range histogram {
		if b.Count > maxCount {
			maxCount = b.Count
		}
	}

	return maxCount
}

// heatmapColor picks the bar color for a bucket based on its share of maxCount.
func heatmapColor(ratio float64) string {
	switch {
	case ratio > 0.75:
		return ColorRed
	case ratio > 0.3:
		return ColorYellow
	case ratio > 0.1:
		return ColorGreen
	default:
		return ColorBlue
	}
}

// printHeatmapBucket renders one non-empty histogram bucket: its range label, count,
// a proportional bar, and its share of opsTotal.
func printHeatmapBucket(w *tabwriter.Writer, b Bucket, maxCount int, opsTotal uint64) {
	// 1. Draw Bar
	barLen := 0
	if maxCount > 0 {
		barLen = (b.Count * 40) / maxCount
	}

	ratio := 0.0
	if maxCount > 0 {
		ratio = float64(b.Count) / float64(maxCount)
	}

	// Heat Color Logic (RESTORED)
	color := heatmapColor(ratio)

	bar := ""
	var barSb619 strings.Builder
	for range barLen {
		barSb619.WriteString("█")
	}
	bar += barSb619.String()

	// 2. Format Label
	label := fmt.Sprintf("%v-%v", b.LowBound, b.HighBound)
	if b.HighBound-b.LowBound < time.Microsecond {
		label = fmt.Sprintf("%dns-%dns", b.LowBound.Nanoseconds(), b.HighBound.Nanoseconds())
	}

	percentage := 0.0
	if opsTotal > 0 {
		percentage = (float64(b.Count) / float64(opsTotal)) * 100
	}

	writef(w, " %s\t%d\t%s%s %s(%.1f%%)\n",
		label, b.Count, color, bar, ColorReset, percentage,
	)
}

// printAnalysis prints the analysis and recommendations section.
func (r Result) printAnalysis(w *tabwriter.Writer, cvPct float64, tailRatio float64) {
	writeLine(w, "")
	writeLine(w, ColorBlue+"--- Analysis & Recommendations ---"+ColorReset)

	// 1. Sample Size Check
	if r.OpsTotal < 5000 {
		writef(w, "%s[WARN] Low sample size (%d). Results may not be statistically significant. Run for longer.%s\n",
			ColorRed, r.OpsTotal, ColorReset)
	}

	// 2. Duration Check
	if r.Duration < 1*time.Second {
		writef(w, "%s[WARN] Test ran for less than 1s. Go runtime/scheduler might not have stabilized.%s\n",
			ColorYellow, ColorReset)
	}

	// 3. Variance Check
	if cvPct > 20.0 {
		writef(w, "%s[FAIL] High Variance (CV %.2f%%). System noise is affecting results. Isolate the machine or increase duration.%s\n",
			ColorRed, cvPct, ColorReset)
	}

	// 4. Memory Check
	if r.AllocsPerOp > 100 {
		writef(w, "%s[INFO] High Allocations (%d/op). This will trigger frequent GC cycles and increase Max Latency.%s\n",
			ColorYellow, r.AllocsPerOp, ColorReset)
	}

	// 5. Outlier Check
	if tailRatio > 20.0 {
		writef(w, "%s[CRITICAL] Massive Latency Spikes Detected. Max is %.0fx higher than P99. Check for Stop-The-World GC or Lock Contention.%s\n",
			ColorRed, tailRatio, ColorReset)
	}

	// 6. Error Check
	if r.ErrorRate > 1.0 {
		writef(w, "%s[FAIL] High Error Rate (%.2f%%). System is failing under load.%s\n", ColorRed, r.ErrorRate, ColorReset)
	}

	if cvPct < 10.0 && r.OpsTotal > 10000 && tailRatio < 10.0 && r.ErrorRate == 0 {
		writef(w, "%s[PASS] RunBenchmark looks healthy and statistically sound.%s\n", ColorGreen, ColorReset)
	}
	writeLine(w, "----------------------------------")
}

// --- HELPER FUNCTIONS ---

func writef(w *tabwriter.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func writeLine(w *tabwriter.Writer, s string) {
	_, _ = fmt.Fprintln(w, s)
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}

	pos := p * float64(len(sorted)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))

	if lower == upper {
		return sorted[lower]
	}

	fraction := pos - float64(lower)
	valLower := float64(sorted[lower])
	valUpper := float64(sorted[upper])

	return time.Duration(valLower + fraction*(valUpper-valLower))
}

func calcHistogramImproved(latencies []time.Duration, min, max time.Duration, buckets int) []Bucket {
	if len(latencies) == 0 {
		return nil
	}
	if min <= 0 {
		min = 1
	}
	if max < min {
		max = min
	}

	res := make([]Bucket, buckets)

	// Geometric series: min * factor^N = max
	factor := math.Pow(float64(max)/float64(min), 1.0/float64(buckets))

	// Avoid degenerate case
	if factor <= 1.0 {
		factor = 1.00001
	}

	currLower := float64(min)
	for i := range buckets {
		currUpper := currLower * factor
		if i == buckets-1 {
			currUpper = float64(max)
		}
		res[i] = Bucket{
			LowBound:  time.Duration(currLower),
			HighBound: time.Duration(currUpper),
		}
		currLower = currUpper
	}

	logMin := math.Log(float64(min))
	logFactor := math.Log(factor)

	for _, lat := range latencies {
		val := float64(lat)
		if val < float64(min) {
			res[0].Count++

			continue
		}

		idx := int((math.Log(val) - logMin) / logFactor)
		if idx < 0 {
			idx = 0
		}
		if idx >= buckets {
			idx = buckets - 1
		}
		res[idx].Count++
	}

	return res
}

func printSparkline(w *tabwriter.Writer, timeline []TimePoint) {
	if len(timeline) == 0 {
		return
	}

	maxOps := 0.0
	for _, p := range timeline {
		if p.OpsSec > maxOps {
			maxOps = p.OpsSec
		}
	}

	blocks := []string{" ", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	fmt.Print("Timeline: [")
	for _, p := range timeline {
		if maxOps == 0 {
			writef(w, " ")

			continue
		}
		ratio := p.OpsSec / maxOps
		idx := max(int(ratio*float64(len(blocks)-1)), 0)
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}

		color := ColorGreen
		if ratio < 0.5 {
			color = ColorYellow
		}
		if ratio < 0.2 {
			color = ColorRed
		}
		writef(w, "%s%s%s", color, blocks[idx], ColorReset)
	}
	writef(w, "] (Max: %.0f ops/s)\n", maxOps)
}
