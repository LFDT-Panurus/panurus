/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	"golang.org/x/exp/trace"
)

// GoroutineStats tracks the lifecycle and issues for a single goroutine.
type GoroutineStats struct {
	ID            trace.GoID
	StartTime     trace.Time
	EndTime       trace.Time
	IsRunning     bool
	BlockedTime   time.Duration
	BlockReasons  map[string]time.Duration
	CreationStack string // The code that created this goroutine (creator's stack)

	// CPU / scheduler data.
	CPUTime       time.Duration // Total time in executing states (running + syscall)
	RunningTime   time.Duration // Time in GoRunning
	SyscallTime   time.Duration // Time in GoSyscall
	LastExecState trace.GoState

	// Per-goroutine runnable-queue wait.
	RunnableWait      time.Duration
	RunnableWaitCount int64
}

// AnalysisOptions holds the parameters for the trace analysis.
type AnalysisOptions struct {
	LatencyThreshold time.Duration
	BlockThreshold   time.Duration
}

// Result holds the aggregated results of a trace analysis.
type Result struct {
	GoroutineStats map[trace.GoID]*GoroutineStats
	TotalLatency   time.Duration
	LatencyCount   int64
	MaxLatency     time.Duration
	TraceEndTime   trace.Time
}

// Print displays the full summary report from the analysis results.
func (r *Result) Print() {
	fmt.Println("---------------------------------------------------")
	fmt.Println("SUMMARY REPORT")
	fmt.Println("---------------------------------------------------")
	r.printLeakSummary()
	r.printLatencySummary()
	r.printBlockingSummary()
	r.printCPUSummary()
}

func (r *Result) printLeakSummary() {
	activeCount := 0
	var activeGoroutines []string
	for _, stat := range r.GoroutineStats {
		if stat.IsRunning {
			activeCount++
			if len(activeGoroutines) < 5 {
				activeGoroutines = append(activeGoroutines, fmt.Sprintf("  [LEAK?] G%d created at: %s", stat.ID, stat.CreationStack))
			}
		}
	}

	fmt.Println("Suspiciously Active Goroutines (Potential Leaks):")
	if activeCount == 0 {
		fmt.Println("  None detected.")
	} else {
		for _, line := range activeGoroutines {
			fmt.Println(line)
		}
		if activeCount > len(activeGoroutines) {
			fmt.Printf("  ... and %d more.\n", activeCount-len(activeGoroutines))
		}
	}
}

func (r *Result) printLatencySummary() {
	fmt.Println()
	if r.LatencyCount > 0 {
		avgLatency := r.TotalLatency / time.Duration(r.LatencyCount)
		fmt.Printf("Avg Scheduler Latency: %v over %d transitions\n", avgLatency, r.LatencyCount)
		fmt.Printf("Max Scheduler Latency: %v\n", r.MaxLatency)
	} else {
		fmt.Println("No runnable->running transitions observed.")
	}
}

func (r *Result) printBlockingSummary() {
	type blockEntry struct {
		ID       trace.GoID
		Duration time.Duration
		Reason   string
		Stack    string
	}

	var blockedList []blockEntry
	for _, stat := range r.GoroutineStats {
		if stat.BlockedTime > 0 {
			maxReason, maxDur := "", time.Duration(0)
			for r, d := range stat.BlockReasons {
				if d > maxDur {
					maxDur, maxReason = d, r
				}
			}
			blockedList = append(blockedList, blockEntry{stat.ID, stat.BlockedTime, maxReason, stat.CreationStack})
		}
	}

	sort.Slice(blockedList, func(i, j int) bool { return blockedList[i].Duration > blockedList[j].Duration })

	fmt.Println("\nTop 5 Most Blocked Goroutines:")
	if len(blockedList) == 0 {
		fmt.Println("  None with measurable blocking.")
	} else {
		limit := min(len(blockedList), 5)
		for i := range limit {
			e := blockedList[i]
			fmt.Printf("  G%d: %v blocked (most time on: %s)\n    Created at: %s\n", e.ID, e.Duration, e.Reason, e.Stack)
		}
	}
}

// cpuStatEntry is one goroutine's row in printCPUSummary's top-CPU-consumers
// summary.
type cpuStatEntry struct {
	ID              trace.GoID
	CPUTime         time.Duration
	RunningTime     time.Duration
	SyscallTime     time.Duration
	Lifetime        time.Duration
	AvgRunnableWait time.Duration
	CreationStack   string
}

// buildCPUStatEntry builds a cpuStatEntry from stat, computing its lifetime
// (falling back to traceEndTime when the goroutine never exited) and average
// runnable-wait latency. ok is false when stat has no measurable CPU time,
// in which case it should be excluded from the summary.
func buildCPUStatEntry(stat *GoroutineStats, traceEndTime trace.Time) (entry cpuStatEntry, ok bool) {
	if stat.CPUTime <= 0 {
		return cpuStatEntry{}, false
	}

	var lifetime time.Duration
	if stat.StartTime != 0 {
		end := stat.EndTime
		if end == 0 && traceEndTime != 0 {
			end = traceEndTime
		}
		if end > stat.StartTime {
			lifetime = time.Duration(end - stat.StartTime)
		}
	}
	var avgWait time.Duration
	if stat.RunnableWaitCount > 0 {
		avgWait = stat.RunnableWait / time.Duration(stat.RunnableWaitCount)
	}

	return cpuStatEntry{stat.ID, stat.CPUTime, stat.RunningTime, stat.SyscallTime, lifetime, avgWait, stat.CreationStack}, true
}

func (r *Result) printCPUSummary() {
	var cpuList []cpuStatEntry
	for _, stat := range r.GoroutineStats {
		if entry, ok := buildCPUStatEntry(stat, r.TraceEndTime); ok {
			cpuList = append(cpuList, entry)
		}
	}

	sort.Slice(cpuList, func(i, j int) bool { return cpuList[i].CPUTime > cpuList[j].CPUTime })

	fmt.Println("\nTop 5 CPU-Heavy Goroutines:")
	if len(cpuList) == 0 {
		fmt.Println("  No goroutines with measurable CPU time.")
	} else {
		limit := min(len(cpuList), 5)
		for i := range limit {
			e := cpuList[i]
			fmt.Printf("  G%d: CPU=%v (run=%v, sys=%v), lifetime≈%v, avg run-q wait≈%v\n    Created at: %s\n",
				e.ID, e.CPUTime, e.RunningTime, e.SyscallTime, e.Lifetime, e.AvgRunnableWait, e.CreationStack)
		}
	}
}

// analysisState holds the live state during the analysis of the trace.
type analysisState struct {
	opts     AnalysisOptions
	stats    map[trace.GoID]*GoroutineStats
	runnable map[trace.GoID]trace.Time
	blocked  map[trace.GoID]struct {
		Time   trace.Time
		Reason string
		Stack  trace.Stack
	}
	execStart     map[trace.GoID]trace.Time
	totalLatency  time.Duration
	latencyCount  int64
	maxLatency    time.Duration
	lastEventTime trace.Time
}

// analyze reads and processes a Go execution trace from an io.Reader.
func analyze(r io.Reader, opts AnalysisOptions) (*Result, error) {
	traceReader, err := trace.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace reader: %w", err)
	}

	state := &analysisState{
		opts:     opts,
		stats:    make(map[trace.GoID]*GoroutineStats),
		runnable: make(map[trace.GoID]trace.Time),
		blocked: make(map[trace.GoID]struct {
			Time   trace.Time
			Reason string
			Stack  trace.Stack
		}),
		execStart: make(map[trace.GoID]trace.Time),
	}

	for {
		ev, err := traceReader.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read event: %w", err)
		}
		processEvent(ev, state)
	}

	// Finalize any open intervals at the end of the trace.
	finalizeCPUAccounting(state)

	return &Result{
		GoroutineStats: state.stats,
		TotalLatency:   state.totalLatency,
		LatencyCount:   state.latencyCount,
		MaxLatency:     state.maxLatency,
		TraceEndTime:   state.lastEventTime,
	}, nil
}

// processEvent handles a single trace event.
func processEvent(ev trace.Event, state *analysisState) {
	state.lastEventTime = ev.Time()

	if ev.Kind() != trace.EventStateTransition {
		return
	}

	st := ev.StateTransition()
	if st.Resource.Kind != trace.ResourceGoroutine {
		return
	}

	handleGoroutineTransition(ev, st, state)
}

// handleGoroutineTransition processes a state transition for a goroutine.
func handleGoroutineTransition(ev trace.Event, st trace.StateTransition, state *analysisState) {
	id := st.Resource.Goroutine()
	from, to := st.Goroutine()

	// Ensure stats struct exists.
	if _, exists := state.stats[id]; !exists {
		state.stats[id] = &GoroutineStats{
			ID:           id,
			BlockReasons: make(map[string]time.Duration),
		}
	}
	gs := state.stats[id]

	// Update CPU time accounting.
	updateCPUStats(ev, id, from, to, gs, state)

	// Lifecycle: Creation and termination.
	if from == trace.GoNotExist && to == trace.GoRunnable {
		gs.StartTime = ev.Time()
		gs.IsRunning = true
		gs.CreationStack = formatStack(ev.Stack()) // Creator's stack.
	}
	if to == trace.GoNotExist {
		gs.EndTime = ev.Time()
		gs.IsRunning = false
		delete(state.runnable, id)
		delete(state.blocked, id)
		delete(state.execStart, id)
	}

	// Scheduler latency analysis.
	analyzeSchedulerLatency(ev, st, id, from, to, gs, state)

	// Blocking analysis.
	analyzeBlocking(ev, st, id, from, to, gs, state)
}

// updateCPUStats updates the time a goroutine spends in an executing state.
func updateCPUStats(ev trace.Event, id trace.GoID, from, to trace.GoState, gs *GoroutineStats, state *analysisState) {
	if from.Executing() {
		if start, ok := state.execStart[id]; ok {
			dur := ev.Time().Sub(start)
			gs.CPUTime += dur
			switch from {
			case trace.GoRunning:
				gs.RunningTime += dur
			case trace.GoSyscall:
				gs.SyscallTime += dur
			}
		}
		delete(state.execStart, id)
	}

	if to.Executing() {
		state.execStart[id] = ev.Time()
		gs.LastExecState = to
	}
}

// analyzeSchedulerLatency measures the time a goroutine spends in the runnable queue.
func analyzeSchedulerLatency(ev trace.Event, st trace.StateTransition, id trace.GoID, from, to trace.GoState, gs *GoroutineStats, state *analysisState) {
	if to == trace.GoRunnable {
		state.runnable[id] = ev.Time()
	}

	if from == trace.GoRunnable && to == trace.GoRunning {
		if startWait, ok := state.runnable[id]; ok {
			waitDuration := ev.Time().Sub(startWait)
			state.totalLatency += waitDuration
			state.latencyCount++
			if waitDuration > state.maxLatency {
				state.maxLatency = waitDuration
			}
			gs.RunnableWait += waitDuration
			gs.RunnableWaitCount++

			if waitDuration > state.opts.LatencyThreshold {
				fmt.Printf("[SCHEDULER SLOW] G%d waited %v to run.\n  Resume at: %s\n",
					id, waitDuration, formatStack(st.Stack))
			}
		}
		delete(state.runnable, id)
	}
}

// analyzeBlocking measures the time a goroutine spends in a blocked state.
func analyzeBlocking(ev trace.Event, st trace.StateTransition, id trace.GoID, from, to trace.GoState, gs *GoroutineStats, state *analysisState) {
	if to == trace.GoWaiting {
		state.blocked[id] = struct {
			Time   trace.Time
			Reason string
			Stack  trace.Stack
		}{ev.Time(), st.Reason, st.Stack}
	}

	if from == trace.GoWaiting {
		if blockInfo, ok := state.blocked[id]; ok {
			blockDuration := ev.Time().Sub(blockInfo.Time)
			gs.BlockedTime += blockDuration
			gs.BlockReasons[blockInfo.Reason] += blockDuration

			isSyncBlock := strings.Contains(blockInfo.Reason, "Sync") || strings.Contains(blockInfo.Reason, "Mutex") || strings.Contains(blockInfo.Reason, "chan")
			if isSyncBlock && blockDuration > state.opts.BlockThreshold {
				fmt.Printf("[CONTENTION] G%d blocked for %v on %s.\n  Blocked at: %s\n",
					id, blockDuration, blockInfo.Reason, formatStack(blockInfo.Stack))
			}
			delete(state.blocked, id)
		}
	}
}

// finalizeCPUAccounting adds CPU time for goroutines still executing at the end of the trace.
func finalizeCPUAccounting(state *analysisState) {
	for id, start := range state.execStart {
		gs, ok := state.stats[id]
		if !ok || state.lastEventTime == 0 || start == 0 {
			continue
		}
		dur := state.lastEventTime.Sub(start)
		gs.CPUTime += dur
		switch gs.LastExecState {
		case trace.GoRunning:
			gs.RunningTime += dur
		case trace.GoSyscall:
			gs.SyscallTime += dur
		}
	}
}

// formatStack formats the top relevant frame of a stack trace.
func formatStack(st trace.Stack) string {
	var frames []trace.StackFrame
	for f := range st.Frames() {
		frames = append(frames, f)
	}
	if len(frames) == 0 {
		return "(no stack)"
	}
	for _, f := range frames {
		if !strings.Contains(f.File, "runtime/") && !strings.Contains(f.File, "internal/") {
			return fmt.Sprintf("%s (%s:%d)", f.Func, f.File, f.Line)
		}
	}
	f := frames[0]

	return fmt.Sprintf("%s (%s:%d)", f.Func, f.File, f.Line)
}

// main is the entry point of the application. It handles flag parsing,
// file opening, and orchestrates the analysis and printing of results.
func main() {
	tracePath := flag.String("file", "trace.out", "Path to the Go execution trace file")
	latencyThreshold := flag.Duration("latency", 10*time.Millisecond, "Threshold for scheduler latency warnings")
	blockThreshold := flag.Duration("block", 10*time.Millisecond, "Threshold for mutex blocking warnings")
	flag.Parse()

	opts := AnalysisOptions{
		LatencyThreshold: *latencyThreshold,
		BlockThreshold:   *blockThreshold,
	}

	f, err := os.Open(*tracePath)
	if err != nil {
		log.Fatalf("failed to open trace file: %v", err)
	}
	defer func() {
		_ = f.Close()
	}()

	fmt.Printf("Analyzing %s...\n", *tracePath)
	fmt.Println("---------------------------------------------------")

	result, err := analyze(f, opts)
	if err != nil {
		log.Fatalf("Analysis failed: %v", err)
	}

	result.Print()
}
