/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

// Package tracer provides hierarchical call tracing and performance profiling for Go applications.
//
// The tracer supports two modes:
//   - Discovery mode: Captures function calls to identify code paths for instrumentation
//   - Profiling mode: Records detailed timing information and builds call hierarchies
//
// Usage:
//
//	tracer.Enable()
//	defer tracer.Disable()
//	// ... your code with tracer.Enter() calls ...
//	tracer.PrintWithOptions(tracer.PrintOptions{
//	    RootFunction: "MyFunction",
//	    ShowPercent: true,
//	    ShowAbsolute: true,
//	})
package tracer

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	enabled        bool            // Global flag to enable/disable tracing
	mu             sync.Mutex      // Protects concurrent access to tracer state
	callStack      []*CallNode     // Current call stack during execution
	rootNode       *CallNode       // Root of the call tree
	capturedStacks map[string]bool // Set of discovered function names (discovery mode)
	discoveryMode  bool            // Flag indicating discovery vs profiling mode
)

// CallNode represents a single function call in the call hierarchy.
// It tracks timing, parent-child relationships, and call counts for aggregation.
type CallNode struct {
	Name      string        // Function name (e.g., "(*Type).Method")
	StartTime time.Time     // When the function was entered
	Duration  time.Duration // Total time spent in this function
	Children  []*CallNode   // Child function calls
	Parent    *CallNode     // Parent function call
	CallCount int           // Number of times this function was called (for aggregation)
}

// PrintOptions configures the output format for call hierarchy printing.
type PrintOptions struct {
	RootFunction   string  // Start printing from this function (empty = use actual root)
	ShowPercent    bool    // Show percentage of time relative to root
	ShowAbsolute   bool    // Show absolute time duration
	AggregateLoops bool    // Combine multiple calls to same function
	MinPercentage  float64 // Hide functions below this percentage threshold
}

// Enable activates the tracer in profiling mode.
// Call this before the code you want to profile, and Disable() when done.
func Enable() {
	mu.Lock()
	defer mu.Unlock()
	enabled = true
	callStack = nil
	rootNode = nil
	capturedStacks = make(map[string]bool)
	discoveryMode = false
}

// EnableDiscovery activates the tracer in discovery mode.
// In this mode, the tracer captures function names without detailed timing,
// useful for identifying which packages need instrumentation.
func EnableDiscovery() {
	mu.Lock()
	defer mu.Unlock()
	enabled = true
	discoveryMode = true
	callStack = nil
	rootNode = nil
	capturedStacks = make(map[string]bool)
}

// Disable deactivates the tracer and clears its state.
// Call this after profiling is complete, typically in a defer statement.
func Disable() {
	mu.Lock()
	defer mu.Unlock()
	enabled = false
	discoveryMode = false
}

// IsEnabled returns true if the tracer is currently active.
func IsEnabled() bool {
	mu.Lock()
	defer mu.Unlock()

	return enabled
}

// IsDiscoveryMode returns true if the tracer is in discovery mode.
func IsDiscoveryMode() bool {
	mu.Lock()
	defer mu.Unlock()

	return discoveryMode
}

// Enter records entry into a function and returns a function to call on exit.
// This should be called at the beginning of instrumented functions using defer:
//
//	func MyFunction() {
//	    defer tracer.Enter("MyFunction")()
//	    // ... function body ...
//	}
//
// The function name should include the receiver type for methods:
//
//	defer tracer.Enter("(*MyType).MyMethod")()
func Enter(funcName string) func() {
	if !IsEnabled() {
		return func() {}
	}

	// In discovery mode, capture full call stack
	if IsDiscoveryMode() {
		captureCallStack()
	}

	mu.Lock()
	node := &CallNode{
		Name:      funcName,
		StartTime: time.Now(),
		CallCount: 1,
	}

	if len(callStack) > 0 {
		parent := callStack[len(callStack)-1]
		node.Parent = parent
		parent.Children = append(parent.Children, node)
	} else {
		rootNode = node
	}

	callStack = append(callStack, node)
	mu.Unlock()

	return func() {
		mu.Lock()
		defer mu.Unlock()

		if len(callStack) > 0 {
			node := callStack[len(callStack)-1]
			node.Duration = time.Since(node.StartTime)
			callStack = callStack[:len(callStack)-1]
		}
	}
}

func captureCallStack() {
	const maxDepth = 100
	pcs := make([]uintptr, maxDepth)
	n := runtime.Callers(3, pcs) // Skip runtime.Callers, captureCallStack, Enter

	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()

		// Only capture panurus functions
		if strings.Contains(frame.Function, "github.com/LFDT-Panurus/panurus/") {
			mu.Lock()
			capturedStacks[frame.Function] = true
			mu.Unlock()
		}

		if !more {
			break
		}
	}
}

// GetDiscoveredPackages returns all unique packages discovered during tracing
func GetDiscoveredPackages() []string {
	mu.Lock()
	defer mu.Unlock()

	packages := make(map[string]bool)

	for funcName := range capturedStacks {
		// Extract package path from function name
		// Format: github.com/LFDT-Panurus/panurus/package/path.(*Type).Method
		// or: github.com/LFDT-Panurus/panurus/package/path.Function

		// Remove github.com/LFDT-Panurus/panurus/ prefix
		path := strings.TrimPrefix(funcName, "github.com/LFDT-Panurus/panurus/")

		// Find the last slash before the function/type name
		lastSlash := strings.LastIndex(path, "/")
		if lastSlash > 0 {
			// Check if there's a dot after the last slash (package.Function)
			remaining := path[lastSlash+1:]
			dotIdx := strings.Index(remaining, ".")
			if dotIdx > 0 {
				// This is the package name
				pkg := path[:lastSlash+1+dotIdx]
				// Remove any generic type parameters
				pkg = strings.Split(pkg, "[")[0]
				packages[pkg] = true
			}
		} else {
			// No slash, just get package before first dot
			dotIdx := strings.Index(path, ".")
			if dotIdx > 0 {
				pkg := path[:dotIdx]
				packages[pkg] = true
			}
		}
	}

	// Convert to sorted slice and clean up
	var result []string
	for pkg := range packages {
		// Remove trailing dots and clean up
		pkg = strings.TrimSuffix(pkg, ".")
		if pkg != "" && !strings.Contains(pkg, "tracer") && !strings.HasSuffix(pkg, "_test") {
			result = append(result, pkg)
		}
	}
	sort.Strings(result)

	return result
}

func Print() {
	PrintWithOptions(PrintOptions{
		ShowAbsolute: true,
	})
}

func PrintWithOptions(opts PrintOptions) {
	mu.Lock()
	defer mu.Unlock()

	if rootNode == nil {
		fmt.Println("No trace data available")

		return
	}

	startNode := rootNode
	if opts.RootFunction != "" {
		startNode = findNode(rootNode, opts.RootFunction)
		if startNode == nil {
			fmt.Printf("Root function '%s' not found in trace\n", opts.RootFunction)

			return
		}
	}

	if opts.AggregateLoops {
		aggregateNode(startNode)
	}

	// Collect top 20 functions for highlighting
	topFunctions := getTopFunctions(startNode, 20)

	fmt.Println("\n## Call Hierarchy")
	fmt.Println("```")
	printNodeWithHighlight(startNode, "", true, startNode.Duration, opts, topFunctions)
	fmt.Println("```")

	// Print Top 20 Functions table
	printTopFunctions(startNode, 20)
}

// FunctionStats holds timing statistics for a function
type FunctionStats struct {
	Name     string
	Duration time.Duration
	Percent  float64
}

func getTopFunctions(root *CallNode, limit int) map[string]bool {
	// Collect all functions with their total time
	stats := make(map[string]*FunctionStats)
	collectStats(root, root.Duration, stats)

	// Convert to slice and sort by duration
	var statsList []*FunctionStats
	for _, stat := range stats {
		statsList = append(statsList, stat)
	}

	sort.Slice(statsList, func(i, j int) bool {
		return statsList[i].Duration > statsList[j].Duration
	})

	// Get top N function names
	if len(statsList) > limit {
		statsList = statsList[:limit]
	}

	topFuncs := make(map[string]bool)
	for _, stat := range statsList {
		topFuncs[stat.Name] = true
	}

	return topFuncs
}

func printTopFunctions(root *CallNode, limit int) {
	// Collect all functions with their total time
	stats := make(map[string]*FunctionStats)
	collectStats(root, root.Duration, stats)

	// Convert to slice and sort by duration
	var statsList []*FunctionStats
	for _, stat := range stats {
		statsList = append(statsList, stat)
	}

	sort.Slice(statsList, func(i, j int) bool {
		return statsList[i].Duration > statsList[j].Duration
	})

	// Print top N
	if len(statsList) > limit {
		statsList = statsList[:limit]
	}

	fmt.Printf("\n## Top %d Functions by Time\n\n", len(statsList))
	fmt.Println("*Note: These functions are marked with `>>>` in the call tree above*")
	fmt.Println()
	fmt.Println("| Function | Total Time | % of Root |")
	fmt.Println("|----------|------------|-----------|")

	for _, stat := range statsList {
		fmt.Printf("| `%s` | %v | %.2f%% |\n", stat.Name, stat.Duration, stat.Percent)
	}
}

func collectStats(node *CallNode, rootDuration time.Duration, stats map[string]*FunctionStats) {
	if node == nil {
		return
	}

	// Add or update this function's stats
	if existing, ok := stats[node.Name]; ok {
		existing.Duration += node.Duration
	} else {
		stats[node.Name] = &FunctionStats{
			Name:     node.Name,
			Duration: node.Duration,
			Percent:  float64(node.Duration) / float64(rootDuration) * 100,
		}
	}

	// Recurse into children
	for _, child := range node.Children {
		collectStats(child, rootDuration, stats)
	}
}

func findNode(node *CallNode, name string) *CallNode {
	// Try exact match first
	if node.Name == name {
		return node
	}

	// Try substring match (e.g., "VerifyTransfer" matches "(*Unknown).VerifyTransfer")
	if strings.Contains(node.Name, name) {
		return node
	}

	// Search children
	for _, child := range node.Children {
		if found := findNode(child, name); found != nil {
			return found
		}
	}

	return nil
}

func aggregateNode(node *CallNode) {
	if len(node.Children) == 0 {
		return
	}

	childMap := make(map[string]*CallNode)
	var uniqueChildren []*CallNode

	for _, child := range node.Children {
		if existing, found := childMap[child.Name]; found {
			existing.CallCount++
			existing.Duration += child.Duration
			existing.Children = append(existing.Children, child.Children...)
		} else {
			childMap[child.Name] = child
			uniqueChildren = append(uniqueChildren, child)
		}
	}

	node.Children = uniqueChildren

	for _, child := range node.Children {
		aggregateNode(child)
	}
}

func printNodeWithHighlight(node *CallNode, prefix string, isLast bool, rootDuration time.Duration, opts PrintOptions, topFunctions map[string]bool) {
	if node == nil {
		return
	}

	percent := (float64(node.Duration) / float64(rootDuration)) * 100
	if opts.MinPercentage > 0 && percent < opts.MinPercentage {
		return
	}

	connector := "├── "
	if isLast {
		connector = "└── "
	}

	callInfo := ""
	if opts.AggregateLoops && node.CallCount > 1 {
		callInfo = fmt.Sprintf(" x%d", node.CallCount)
	}

	timeInfo := ""
	if opts.ShowAbsolute && opts.ShowPercent {
		timeInfo = fmt.Sprintf(" [%v, %.2f%%]", node.Duration, percent)
	} else if opts.ShowAbsolute {
		timeInfo = fmt.Sprintf(" [%v]", node.Duration)
	} else if opts.ShowPercent {
		timeInfo = fmt.Sprintf(" [%.2f%%]", percent)
	}

	// Highlight entire line if in top functions with >>> marker
	if topFunctions[node.Name] {
		fmt.Printf("%s%s>>> %s%s%s\n", prefix, connector, node.Name, callInfo, timeInfo)
	} else {
		fmt.Printf("%s%s%s%s%s\n", prefix, connector, node.Name, callInfo, timeInfo)
	}

	childPrefix := prefix
	if isLast {
		childPrefix += "    "
	} else {
		childPrefix += "│   "
	}

	for i, child := range node.Children {
		printNodeWithHighlight(child, childPrefix, i == len(node.Children)-1, rootDuration, opts, topFunctions)
	}
}

func PrintSummary(topN int) {
	PrintSummaryWithOptions(topN, "", PrintOptions{ShowAbsolute: true, ShowPercent: true})
}

func PrintSummaryWithRoot(topN int, rootFunctionName string) {
	PrintSummaryWithOptions(topN, rootFunctionName, PrintOptions{ShowAbsolute: true, ShowPercent: true})
}

// PrintSummaryWithOptions prints a performance summary with configurable display options.
func PrintSummaryWithOptions(topN int, rootFunctionName string, opts PrintOptions) {
	mu.Lock()
	defer mu.Unlock()

	if rootNode == nil {
		fmt.Println("No trace data available")

		return
	}

	// Find the specified root or use the actual root
	summaryRoot := rootNode
	if rootFunctionName != "" {
		summaryRoot = findNode(rootNode, rootFunctionName)
		if summaryRoot == nil {
			fmt.Printf("Root function '%s' not found, using actual root\n", rootFunctionName)
			summaryRoot = rootNode
		}
	}

	funcTimes := make(map[string]time.Duration)
	collectFunctionTimes(summaryRoot, funcTimes)

	type funcTime struct {
		name     string
		duration time.Duration
	}

	var times []funcTime
	for name, duration := range funcTimes {
		times = append(times, funcTime{name, duration})
	}

	sort.Slice(times, func(i, j int) bool {
		return times[i].duration > times[j].duration
	})

	fmt.Println("\n=== Performance Summary (Top Functions) ===")
	if rootFunctionName != "" {
		fmt.Printf("(Relative to: %s)\n", rootFunctionName)
	}

	// Build header based on display options
	header := "Function"
	lineLength := 60

	if opts.ShowAbsolute {
		header += fmt.Sprintf(" %15s", "Total Time")
		lineLength += 16
	}
	if opts.ShowPercent {
		header += fmt.Sprintf(" %10s", "% of Root")
		lineLength += 11
	}

	fmt.Println(header)
	fmt.Println(strings.Repeat("-", lineLength))

	count := min(topN, len(times))

	for i := range count {
		percent := (float64(times[i].duration) / float64(summaryRoot.Duration)) * 100

		// Build output based on display options
		output := fmt.Sprintf("%-60s", times[i].name)
		if opts.ShowAbsolute {
			output += fmt.Sprintf(" %15v", times[i].duration)
		}
		if opts.ShowPercent {
			output += fmt.Sprintf(" %9.2f%%", percent)
		}
		fmt.Println(output)
	}
}

func collectFunctionTimes(node *CallNode, funcTimes map[string]time.Duration) {
	if node == nil {
		return
	}

	funcTimes[node.Name] += node.Duration

	for _, child := range node.Children {
		collectFunctionTimes(child, funcTimes)
	}
}

func GetCallHierarchy() string {
	mu.Lock()
	defer mu.Unlock()

	if rootNode == nil {
		return "No trace data available"
	}

	var sb strings.Builder
	buildHierarchy(rootNode, "", true, &sb)

	return sb.String()
}

func buildHierarchy(node *CallNode, prefix string, isLast bool, sb *strings.Builder) {
	if node == nil {
		return
	}

	connector := "├── "
	if isLast {
		connector = "└── "
	}

	fmt.Fprintf(sb, "%s%s%s\n", prefix, connector, node.Name)

	childPrefix := prefix
	if isLast {
		childPrefix += "    "
	} else {
		childPrefix += "│   "
	}

	for i, child := range node.Children {
		buildHierarchy(child, childPrefix, i == len(node.Children)-1, sb)
	}
}
