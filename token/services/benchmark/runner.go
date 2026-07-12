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

// RunBenchmark executes the benchmark.
func RunBenchmark[T any](
	cfg Config,
	setup func() T,
	work func(T) error,
) Result {
	// Sanity defaults
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 1 * time.Second
	}

	// ---------------------------------------------------------
	// PHASE 1: Memory Analysis (Serial & Isolated)
	// ---------------------------------------------------------
	memBytes, memAllocs := measureMemory(setup, work)

	// ---------------------------------------------------------
	// PHASE 2: Throughput & Latency (Concurrent)
	// ---------------------------------------------------------
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	var (
		running        atomic.Bool
		recording      atomic.Bool
		opsCounter     atomic.Uint64
		startWg        sync.WaitGroup
		endWg          sync.WaitGroup
		startGlobal    time.Time
		peakGoroutines atomic.Int64
	)

	running.Store(true)
	recording.Store(false)
	workerResults := make([]workerStats, cfg.Workers)

	// Rate Limiter Calculation
	var intervalPerOp time.Duration
	if cfg.RateLimit > 0 {
		ratePerWorker := cfg.RateLimit / float64(cfg.Workers)
		if ratePerWorker > 0 {
			intervalPerOp = time.Duration(float64(time.Second) / ratePerWorker)
		}
	}

	startWg.Add(cfg.Workers)
	endWg.Add(cfg.Workers)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := range cfg.Workers {
		workerID := i
		go func() {
			defer endWg.Done()

			currentChunk := &chunk{}
			headChunk := currentChunk
			var localErrors uint64
			var nextTick time.Time

			if intervalPerOp > 0 {
				nextTick = time.Now()
			}

			// This acts as a "starting gun" (Barrier pattern).
			// It ensures that all worker goroutines are spawned, initialized, and ready to go before any of them begin execution.
			startWg.Done()
			startWg.Wait()

			d := setup()

			for running.Load() {
				// Open-Loop Throttling
				if intervalPerOp > 0 {
					now := time.Now()
					if now.Before(nextTick) {
						time.Sleep(nextTick.Sub(now))
					}
					nextTick = nextTick.Add(intervalPerOp)
					if time.Since(nextTick) > intervalPerOp*10 {
						nextTick = time.Now()
					}
				}

				tStart := time.Now()
				err := work(d)
				dur := time.Since(tStart)

				if recording.Load() {
					// STRICT CHECK: Ensure op started AFTER recording began
					if tStart.After(startGlobal) {
						opsCounter.Add(1)
						if err != nil {
							localErrors++
						}

						if currentChunk.idx >= chunkSize {
							newC := &chunk{}
							currentChunk.next = newC
							currentChunk = newC
						}
						currentChunk.data[currentChunk.idx] = dur
						currentChunk.idx++
					}
				}
			}
			workerResults[workerID] = workerStats{head: headChunk, errors: localErrors}
		}()
	}

	startWg.Wait()

	if cfg.WarmupDuration > 0 {
		time.Sleep(cfg.WarmupDuration)
	}

	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	startGlobal = time.Now()
	recording.Store(true)

	// Timeline Monitor
	timeline := make([]TimePoint, 0, int(cfg.Duration.Seconds())+1)

	var monitorWg sync.WaitGroup
	monitorWg.Add(1)

	// Baseline goroutine count: workers + monitor + main + any framework goroutines.
	// Captured just before recording so executor goroutines are not yet active.
	goroutineBaseline := int64(runtime.NumGoroutine())
	peakGoroutines.Store(goroutineBaseline)

	go func() {
		defer monitorWg.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		var prevOps uint64
		startTime := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				if !running.Load() {
					return
				}
				currOps := opsCounter.Load()
				delta := currOps - prevOps
				prevOps = currOps
				pt := TimePoint{Timestamp: t.Sub(startTime), OpsSec: float64(delta)}
				timeline = append(timeline, pt)

				// Track peak goroutine count during recording.
				// Unbounded executors spawn goroutines per proof that live
				// microseconds — we sample every tick to catch their peak.
				current := int64(runtime.NumGoroutine())
				for {
					old := peakGoroutines.Load()
					if current <= old {
						break
					}
					if peakGoroutines.CompareAndSwap(old, current) {
						break
					}
				}
			}
		}
	}()

	time.Sleep(cfg.Duration)

	running.Store(false)
	endWg.Wait()
	cancel()

	globalDuration := time.Since(startGlobal)
	monitorWg.Wait() // BLOCK here until monitor goroutine returns

	runtime.ReadMemStats(&memAfter)

	goroutinesCreated := max(peakGoroutines.Load()-goroutineBaseline, 0)

	return analyzeResults(cfg, workerResults, memBytes, memAllocs, memBefore, memAfter, globalDuration, timeline, goroutinesCreated)
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

func analyzeResults(
	cfg Config,
	workers []workerStats,
	memBytes, memAllocs uint64,
	mStart, mEnd runtime.MemStats,
	duration time.Duration,
	timeline []TimePoint,
	goroutinesCreated int64,
) Result {
	var totalOps uint64
	var totalErrors uint64
	var totalTimeNs int64

	estimatedOps := uint64(len(workers)) * uint64(chunkSize) * 2
	allLatencies := make([]time.Duration, 0, estimatedOps)

	var totalJitter float64
	var jitterSamples uint64

	for _, w := range workers {
		totalErrors += w.errors
		curr := w.head
		var prevLat time.Duration
		first := true

		for curr != nil {
			limit := curr.idx
			totalOps += uint64(limit) // #nosec G115
			for k := range limit {
				lat := curr.data[k]
				if lat == 0 {
					continue
				}

				totalTimeNs += int64(lat)
				allLatencies = append(allLatencies, lat)

				if !first {
					diff := float64(lat - prevLat)
					if diff < 0 {
						diff = -diff
					}
					totalJitter += diff
					jitterSamples++
				}
				prevLat = lat
				first = false
			}
			curr = curr.next
		}
	}

	if totalOps == 0 {
		return Result{Config: cfg, ErrorRate: 100.0}
	}

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

	jitter := time.Duration(0)
	if jitterSamples > 0 {
		jitter = time.Duration(totalJitter / float64(jitterSamples))
	}

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

	// GC Stats
	numGC := mEnd.NumGC - mStart.NumGC
	pauseNs := mEnd.PauseTotalNs - mStart.PauseTotalNs
	gcOverhead := (float64(pauseNs) / float64(duration.Nanoseconds())) * 100
	allocRate := (float64(mEnd.TotalAlloc-mStart.TotalAlloc) / 1024 / 1024) / duration.Seconds()

	return Result{
		Config:            cfg,
		GoRoutines:        cfg.Workers,
		OpsTotal:          totalOps,
		Duration:          duration,
		OpsPerSecReal:     opsPerSecReal,
		OpsPerSecPure:     opsPerSecPure,
		AvgLatency:        avgLatency,
		StdDevLatency:     stdDev,
		Variance:          variance,
		P50Latency:        p50,
		P75Latency:        p75,
		P95Latency:        p95,
		P99Latency:        p99,
		P999Latency:       p999,
		P9999Latency:      p9999,
		MinLatency:        minLat,
		MaxLatency:        maxLat,
		IQR:               iqr,
		Jitter:            jitter,
		CoeffVar:          coeffVar,
		BytesPerOp:        memBytes,
		AllocsPerOp:       memAllocs,
		AllocRateMBPS:     allocRate,
		NumGC:             numGC,
		GCPauseTotal:      time.Duration(pauseNs), // #nosec G115
		GCOverhead:        gcOverhead,
		ErrorCount:        totalErrors,
		ErrorRate:         (float64(totalErrors) / float64(totalOps)) * 100,
		Histogram:         calcHistogramImproved(allLatencies, minLat, maxLat, 20),
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

	maxCount := 0
	for _, b := range r.Histogram {
		if b.Count > maxCount {
			maxCount = b.Count
		}
	}

	for _, b := range r.Histogram {
		if b.Count == 0 {
			continue
		}

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
		color := ColorBlue
		if ratio > 0.75 {
			color = ColorRed
		} else if ratio > 0.3 {
			color = ColorYellow
		} else if ratio > 0.1 {
			color = ColorGreen
		}

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
		if r.OpsTotal > 0 {
			percentage = (float64(b.Count) / float64(r.OpsTotal)) * 100
		}

		writef(w, " %s\t%d\t%s%s %s(%.1f%%)\n",
			label, b.Count, color, bar, ColorReset, percentage,
		)
	}
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
