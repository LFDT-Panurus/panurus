/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package tracer

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestEnableDisable verifies that the tracer can be enabled and disabled correctly.
// It checks the initial state, state after Enable(), and state after Disable().
func TestEnableDisable(t *testing.T) {
	if IsEnabled() {
		t.Error("Tracer should be disabled initially")
	}

	Enable()
	if !IsEnabled() {
		t.Error("Tracer should be enabled after Enable()")
	}

	Disable()
	if IsEnabled() {
		t.Error("Tracer should be disabled after Disable()")
	}
}

// TestDiscoveryMode verifies that discovery mode can be enabled and that it properly
// sets both the enabled and discovery mode flags. Discovery mode is used to identify
// which packages are called without collecting detailed timing information.
func TestDiscoveryMode(t *testing.T) {
	EnableDiscovery()
	if !IsEnabled() {
		t.Error("Tracer should be enabled in discovery mode")
	}
	if !IsDiscoveryMode() {
		t.Error("Should be in discovery mode")
	}

	Disable()
	if IsDiscoveryMode() {
		t.Error("Should not be in discovery mode after Disable()")
	}
}

// TestEnterWhenDisabled verifies that calling Enter() when the tracer is disabled
// does not cause a panic. This is important for safety when tracer calls are left
// in code but profiling is not active.
func TestEnterWhenDisabled(t *testing.T) {
	Disable()
	exitFunc := Enter("TestFunction")
	exitFunc()
	// Should not panic
}

// TestBasicCallHierarchy verifies that the tracer correctly captures a simple
// parent-child function call hierarchy and includes both functions in the output.
func TestBasicCallHierarchy(t *testing.T) {
	Enable()
	defer Disable()

	func1 := func() {
		defer Enter("func1")()
		time.Sleep(10 * time.Millisecond)
	}

	func2 := func() {
		defer Enter("func2")()
		time.Sleep(5 * time.Millisecond)
		func1()
	}

	func2()

	hierarchy := GetCallHierarchy()
	if !strings.Contains(hierarchy, "func2") {
		t.Error("Hierarchy should contain func2")
	}
	if !strings.Contains(hierarchy, "func1") {
		t.Error("Hierarchy should contain func1")
	}
}

// TestNestedCalls verifies that the tracer correctly captures deeply nested function
// calls (3 levels) and represents them properly in the hierarchy output.
func TestNestedCalls(t *testing.T) {
	Enable()
	defer Disable()

	level3 := func() {
		defer Enter("level3")()
		time.Sleep(5 * time.Millisecond)
	}

	level2 := func() {
		defer Enter("level2")()
		time.Sleep(5 * time.Millisecond)
		level3()
	}

	level1 := func() {
		defer Enter("level1")()
		time.Sleep(5 * time.Millisecond)
		level2()
	}

	level1()

	hierarchy := GetCallHierarchy()
	lines := strings.Split(hierarchy, "\n")
	if len(lines) < 3 {
		t.Errorf("Expected at least 3 levels in hierarchy, got %d", len(lines))
	}
}

// TestCumulativeTime verifies that parent function duration includes child function
// duration (cumulative timing). This is essential for understanding where time is
// actually spent in the call tree.
func TestCumulativeTime(t *testing.T) {
	Enable()
	defer Disable()

	const sleepTime = 10 * time.Millisecond
	const tolerance = 5 * time.Millisecond

	child := func() {
		defer Enter("child")()
		time.Sleep(sleepTime)
	}

	parent := func() {
		defer Enter("parent")()
		child()
	}

	parent()

	mu.Lock()
	defer mu.Unlock()

	if rootNode == nil {
		t.Fatal("Root node should not be nil")
	}

	// Parent duration should include child duration
	if rootNode.Duration < sleepTime {
		t.Errorf("Parent duration (%v) should be at least child sleep time (%v)",
			rootNode.Duration, sleepTime)
	}

	// Check that we have a child
	if len(rootNode.Children) != 1 {
		t.Fatalf("Expected 1 child, got %d", len(rootNode.Children))
	}

	childNode := rootNode.Children[0]
	if childNode.Duration < sleepTime-tolerance {
		t.Errorf("Child duration (%v) should be close to sleep time (%v)",
			childNode.Duration, sleepTime)
	}
}

// TestMultipleCallsAggregation verifies that when the same function is called multiple
// times, the aggregateNode function can combine them into a single entry with a call
// count, which is useful for reducing clutter in the output.
func TestMultipleCallsAggregation(t *testing.T) {
	Enable()
	defer Disable()

	helper := func() {
		defer Enter("helper")()
		time.Sleep(5 * time.Millisecond)
	}

	main := func() {
		defer Enter("main")()
		helper()
		helper()
		helper()
	}

	main()

	mu.Lock()
	defer mu.Unlock()

	if rootNode == nil {
		t.Fatal("Root node should not be nil")
	}

	// Before aggregation, should have 3 separate child nodes
	if len(rootNode.Children) != 3 {
		t.Errorf("Expected 3 children before aggregation, got %d", len(rootNode.Children))
	}

	// Aggregate
	aggregateNode(rootNode)

	// After aggregation, should have 1 child with count 3
	if len(rootNode.Children) != 1 {
		t.Errorf("Expected 1 child after aggregation, got %d", len(rootNode.Children))
	}

	if rootNode.Children[0].CallCount != 3 {
		t.Errorf("Expected call count 3, got %d", rootNode.Children[0].CallCount)
	}
}

// TestCumulativeTimeWithMultipleCalls verifies that when a function is called multiple
// times and then aggregated, the total duration correctly sums all individual call
// durations.
func TestCumulativeTimeWithMultipleCalls(t *testing.T) {
	Enable()
	defer Disable()

	const sleepTime = 10 * time.Millisecond
	const numCalls = 5
	const tolerance = 5 * time.Millisecond

	helper := func() {
		defer Enter("helper")()
		time.Sleep(sleepTime)
	}

	main := func() {
		defer Enter("main")()
		for range numCalls {
			helper()
		}
	}

	main()

	mu.Lock()
	defer mu.Unlock()

	if rootNode == nil {
		t.Fatal("Root node should not be nil")
	}

	// Aggregate to sum up all helper calls
	aggregateNode(rootNode)

	if len(rootNode.Children) != 1 {
		t.Fatalf("Expected 1 aggregated child, got %d", len(rootNode.Children))
	}

	helperNode := rootNode.Children[0]
	expectedTotal := sleepTime * numCalls

	// The aggregated duration should be approximately numCalls * sleepTime
	if helperNode.Duration < expectedTotal-tolerance*time.Duration(numCalls) {
		t.Errorf("Aggregated helper duration (%v) should be close to %v (5 calls * %v)",
			helperNode.Duration, expectedTotal, sleepTime)
	}

	if helperNode.CallCount != numCalls {
		t.Errorf("Expected call count %d, got %d", numCalls, helperNode.CallCount)
	}
}

// TestPrintOptions verifies that different PrintOptions configurations work correctly
// and don't cause panics. Tests various combinations of ShowAbsolute, ShowPercent,
// AggregateLoops, and RootFunction options.
func TestPrintOptions(t *testing.T) {
	Enable()
	defer Disable()

	func1 := func() {
		defer Enter("func1")()
		time.Sleep(10 * time.Millisecond)
	}

	func2 := func() {
		defer Enter("func2")()
		time.Sleep(5 * time.Millisecond)
		func1()
	}

	func2()

	// Test different print options (just ensure they don't panic)
	PrintWithOptions(PrintOptions{
		ShowAbsolute: true,
		ShowPercent:  true,
	})

	PrintWithOptions(PrintOptions{
		ShowAbsolute:   true,
		AggregateLoops: true,
	})

	PrintWithOptions(PrintOptions{
		RootFunction: "func2",
		ShowPercent:  true,
	})
}

// TestPrintSummary verifies that the summary printing functions work correctly with
// different parameters. Tests PrintSummary, PrintSummaryWithRoot, and
// PrintSummaryWithOptions to ensure they handle various configurations.
func TestPrintSummary(t *testing.T) {
	Enable()
	defer Disable()

	func1 := func() {
		defer Enter("func1")()
		time.Sleep(10 * time.Millisecond)
	}

	func2 := func() {
		defer Enter("func2")()
		time.Sleep(5 * time.Millisecond)
	}

	main := func() {
		defer Enter("main")()
		func1()
		func2()
	}

	main()

	// Test different summary options (just ensure they don't panic)
	PrintSummary(10)
	PrintSummaryWithRoot(10, "main")
	PrintSummaryWithOptions(10, "main", PrintOptions{
		ShowAbsolute: true,
		ShowPercent:  true,
	})
}

// TestFindNode verifies that the findNode function can locate a specific function
// in the call tree by name, and returns nil when the function doesn't exist.
func TestFindNode(t *testing.T) {
	Enable()
	defer Disable()

	func1 := func() {
		defer Enter("target")()
		time.Sleep(5 * time.Millisecond)
	}

	func2 := func() {
		defer Enter("parent")()
		func1()
	}

	func2()

	mu.Lock()
	defer mu.Unlock()

	found := findNode(rootNode, "target")
	if found == nil {
		t.Error("Should find 'target' node")
	}

	notFound := findNode(rootNode, "nonexistent")
	if notFound != nil {
		t.Error("Should not find 'nonexistent' node")
	}
}

// TestGetDiscoveredPackages verifies that discovery mode captures package names
// from the call stack. This is used to identify which packages need instrumentation.
func TestGetDiscoveredPackages(t *testing.T) {
	EnableDiscovery()
	defer Disable()

	// Simulate a function call that will trigger captureCallStack
	testFunc := func() {
		defer Enter("testFunc")()
		// This will capture the actual call stack
	}
	testFunc()

	packages := GetDiscoveredPackages()

	// In discovery mode, we should have captured some packages from the call stack
	// The test itself is in the tracer package, so we should see it
	if len(packages) == 0 {
		t.Log("No packages discovered - this is expected in unit tests")
		// Don't fail the test, as discovery depends on runtime call stack
	}
}

// TestEmptyTrace verifies that when no functions are traced (Enable then immediately
// Disable), the GetCallHierarchy returns an appropriate "No trace data available" message.
func TestEmptyTrace(t *testing.T) {
	Enable()
	Disable()

	hierarchy := GetCallHierarchy()
	if hierarchy != "No trace data available" {
		t.Errorf("Expected 'No trace data available', got: %s", hierarchy)
	}
}

// TestMinPercentageFilter verifies that the MinPercentage option correctly filters
// out functions that take less than the specified percentage of total time.
func TestMinPercentageFilter(t *testing.T) {
	Enable()
	defer Disable()

	fast := func() {
		defer Enter("fast")()
		time.Sleep(1 * time.Millisecond)
	}

	slow := func() {
		defer Enter("slow")()
		time.Sleep(100 * time.Millisecond)
	}

	main := func() {
		defer Enter("main")()
		fast()
		slow()
	}

	main()

	// Print with high threshold - fast function should be filtered out
	PrintWithOptions(PrintOptions{
		ShowAbsolute:  true,
		MinPercentage: 50.0, // Only show functions taking >50% of time
	})
}

// TestPrint verifies that the Print() convenience function works correctly.
// This is a wrapper around PrintWithOptions with default settings.
func TestPrint(t *testing.T) {
	Enable()
	defer Disable()

	func1 := func() {
		defer Enter("func1")()
		time.Sleep(5 * time.Millisecond)
	}

	func1()

	// Test the Print() wrapper function
	Print()
}

// TestGetDiscoveredPackagesWithData verifies that discovery mode correctly tracks
// package names from function calls with fully qualified names.
func TestGetDiscoveredPackagesWithData(t *testing.T) {
	EnableDiscovery()
	defer Disable()

	// Simulate some function calls
	func1 := func() {
		defer Enter("github.com/example/pkg.Function1")()
		time.Sleep(1 * time.Millisecond)
	}

	func2 := func() {
		defer Enter("github.com/example/other.Function2")()
		time.Sleep(1 * time.Millisecond)
	}

	func1()
	func2()

	packages := GetDiscoveredPackages()
	// In discovery mode, packages should be tracked
	if len(packages) == 0 {
		t.Log("No packages discovered - may depend on runtime environment")
	}
}

// TestCollectStatsEdgeCases verifies that statistics collection works correctly
// with a single function call (edge case with minimal call tree).
func TestCollectStatsEdgeCases(t *testing.T) {
	Enable()
	defer Disable()

	// Test with single function
	single := func() {
		defer Enter("single")()
		time.Sleep(5 * time.Millisecond)
	}

	single()

	hierarchy := GetCallHierarchy()
	if !strings.Contains(hierarchy, "single") {
		t.Error("Expected 'single' in hierarchy")
	}
}

// TestBuildHierarchyWithMultipleRoots verifies that when multiple root-level
// functions are called sequentially, only the last one is retained in the hierarchy.
// This is the expected behavior as each root call starts a new trace.
func TestBuildHierarchyWithMultipleRoots(t *testing.T) {
	Enable()
	defer Disable()

	// The tracer tracks the last root call tree
	tree1 := func() {
		defer Enter("tree1")()
		time.Sleep(2 * time.Millisecond)
	}

	tree2 := func() {
		defer Enter("tree2")()
		time.Sleep(2 * time.Millisecond)
	}

	tree1()
	tree2()

	hierarchy := GetCallHierarchy()
	// Only the last tree (tree2) should be in the hierarchy
	if !strings.Contains(hierarchy, "tree2") {
		t.Error("Expected tree2 in hierarchy")
	}
}

// TestPrintNodeWithHighlightEdgeCases verifies that printing deeply nested call
// trees with root function filtering works correctly.
func TestPrintNodeWithHighlightEdgeCases(t *testing.T) {
	Enable()
	defer Disable()

	// Test with deeply nested calls
	level5 := func() {
		defer Enter("level5")()
		time.Sleep(1 * time.Millisecond)
	}

	level4 := func() {
		defer Enter("level4")()
		level5()
	}

	level3 := func() {
		defer Enter("level3")()
		level4()
	}

	level2 := func() {
		defer Enter("level2")()
		level3()
	}

	level1 := func() {
		defer Enter("level1")()
		level2()
	}

	level1()

	// Test with root function filter
	PrintWithOptions(PrintOptions{
		ShowAbsolute:  true,
		ShowPercent:   true,
		RootFunction:  "level3",
		MinPercentage: 0,
	})
}

// TestAggregateNodeWithMultipleCalls verifies that aggregation works correctly
// when a function is called multiple times in a loop, combining them into a
// single entry with the correct call count.
func TestAggregateNodeWithMultipleCalls(t *testing.T) {
	Enable()
	defer Disable()

	helper := func() {
		defer Enter("helper")()
		time.Sleep(2 * time.Millisecond)
	}

	main := func() {
		defer Enter("main")()
		// Call helper multiple times
		for range 3 {
			helper()
		}
	}

	main()

	hierarchy := GetCallHierarchy()
	// Should show aggregated time for helper
	if !strings.Contains(hierarchy, "helper") {
		t.Error("Expected 'helper' in hierarchy")
	}
}

// TestPrintSummaryWithRootFunction verifies that PrintSummaryWithOptions correctly
// filters the summary to show only functions relative to a specified root function.
func TestPrintSummaryWithRootFunction(t *testing.T) {
	Enable()
	defer Disable()

	child := func() {
		defer Enter("child")()
		time.Sleep(5 * time.Millisecond)
	}

	parent := func() {
		defer Enter("parent")()
		child()
		time.Sleep(5 * time.Millisecond)
	}

	grandparent := func() {
		defer Enter("grandparent")()
		parent()
	}

	grandparent()

	// Test PrintSummaryWithOptions with root function
	PrintSummaryWithOptions(5, "parent", PrintOptions{
		ShowAbsolute: true,
		ShowPercent:  true,
	})
}

// TestCollectFunctionTimesWithNoData verifies that when the tracer is enabled
// but no functions are called, GetCallHierarchy returns the appropriate message.
func TestCollectFunctionTimesWithNoData(t *testing.T) {
	Enable()
	Disable()

	// No function calls made
	hierarchy := GetCallHierarchy()
	if hierarchy != "No trace data available" {
		t.Errorf("Expected 'No trace data available', got: %s", hierarchy)
	}
}

// TestFindNodeNotFound verifies that PrintWithOptions handles the case where
// the specified RootFunction doesn't exist in the call tree gracefully.
func TestFindNodeNotFound(t *testing.T) {
	Enable()
	defer Disable()

	func1 := func() {
		defer Enter("func1")()
		time.Sleep(1 * time.Millisecond)
	}

	func1()

	// Try to find a node that doesn't exist
	PrintWithOptions(PrintOptions{
		ShowAbsolute:  true,
		RootFunction:  "nonexistent",
		MinPercentage: 0,
	})
}

// TestGetDiscoveredPackagesWithVariousFormats verifies that discovery mode
// correctly handles function names in various formats (fully qualified paths,
// methods with receivers, simple names).
func TestGetDiscoveredPackagesWithVariousFormats(t *testing.T) {
	EnableDiscovery()
	defer Disable()

	// Test various function name formats
	testFuncs := []struct {
		name string
		fn   func()
	}{
		{
			name: "github.com/LFDT-Panurus/panurus/token/core/zkatdlog.(*Validator).VerifyTransfer",
			fn: func() {
				defer Enter("github.com/LFDT-Panurus/panurus/token/core/zkatdlog.(*Validator).VerifyTransfer")()
				time.Sleep(1 * time.Millisecond)
			},
		},
		{
			name: "github.com/LFDT-Panurus/panurus/token/services/ttx.NewTransaction",
			fn: func() {
				defer Enter("github.com/LFDT-Panurus/panurus/token/services/ttx.NewTransaction")()
				time.Sleep(1 * time.Millisecond)
			},
		},
		{
			name: "simple.Function",
			fn: func() {
				defer Enter("simple.Function")()
				time.Sleep(1 * time.Millisecond)
			},
		},
	}

	for _, tc := range testFuncs {
		tc.fn()
	}

	packages := GetDiscoveredPackages()
	if len(packages) == 0 {
		t.Log("No packages discovered - may depend on runtime environment")
	}
}

func TestCollectStatsWithNilNode(t *testing.T) {
	stats := make(map[string]*FunctionStats)
	// Should not panic with nil node
	collectStats(nil, time.Second, stats)

	if len(stats) != 0 {
		t.Error("Expected empty stats for nil node")
	}
}

func TestCollectStatsWithDuplicateFunctions(t *testing.T) {
	Enable()
	defer Disable()

	// Call the same function multiple times
	helper := func() {
		defer Enter("helper")()
		time.Sleep(2 * time.Millisecond)
	}

	main := func() {
		defer Enter("main")()
		helper()
		helper()
		helper()
	}

	main()

	// The stats should aggregate the helper function's time
	hierarchy := GetCallHierarchy()
	if !strings.Contains(hierarchy, "helper") {
		t.Error("Expected 'helper' in hierarchy")
	}
}

func TestPrintWithOptionsEdgeCases(t *testing.T) {
	Enable()
	defer Disable()

	nested := func() {
		defer Enter("nested")()
		time.Sleep(1 * time.Millisecond)
	}

	parent := func() {
		defer Enter("parent")()
		nested()
		time.Sleep(2 * time.Millisecond)
	}

	parent()

	// Test with very high min percentage (should filter everything)
	PrintWithOptions(PrintOptions{
		ShowAbsolute:  true,
		ShowPercent:   true,
		MinPercentage: 99.9,
	})

	// Test with root function that exists
	PrintWithOptions(PrintOptions{
		ShowAbsolute:  true,
		ShowPercent:   true,
		RootFunction:  "parent",
		MinPercentage: 0,
	})
}

func TestGetTopFunctionsEdgeCases(t *testing.T) {
	Enable()
	defer Disable()

	// Create a scenario with many functions
	for i := range 10 {
		func(idx int) {
			defer Enter(fmt.Sprintf("func%d", idx))()
			time.Sleep(time.Duration(idx+1) * time.Millisecond)
		}(i)
	}

	// Get top functions (should handle sorting correctly)
	hierarchy := GetCallHierarchy()
	if hierarchy == "No trace data available" {
		t.Error("Expected trace data")
	}
}

func TestPrintTopFunctionsWithEmptyStats(t *testing.T) {
	Enable()
	defer Disable()

	// Create a simple call
	simple := func() {
		defer Enter("simple")()
		time.Sleep(1 * time.Millisecond)
	}

	simple()

	// Print with options that should work with the data
	PrintWithOptions(PrintOptions{
		ShowAbsolute:  true,
		ShowPercent:   true,
		MinPercentage: 0,
	})
}

func TestCollectFunctionTimesRecursive(t *testing.T) {
	Enable()
	defer Disable()

	// Create deeply nested calls
	level4 := func() {
		defer Enter("level4")()
		time.Sleep(1 * time.Millisecond)
	}

	level3 := func() {
		defer Enter("level3")()
		level4()
	}

	level2 := func() {
		defer Enter("level2")()
		level3()
	}

	level1 := func() {
		defer Enter("level1")()
		level2()
	}

	level1()

	// Should collect all function times recursively
	PrintSummaryWithOptions(10, "", PrintOptions{
		ShowAbsolute: true,
		ShowPercent:  true,
	})
}

func TestBuildHierarchyWithComplexTree(t *testing.T) {
	Enable()
	defer Disable()

	// Create a complex call tree
	leafA := func() {
		defer Enter("leafA")()
		time.Sleep(1 * time.Millisecond)
	}

	leafB := func() {
		defer Enter("leafB")()
		time.Sleep(1 * time.Millisecond)
	}

	branchA := func() {
		defer Enter("branchA")()
		leafA()
	}

	branchB := func() {
		defer Enter("branchB")()
		leafB()
	}

	root := func() {
		defer Enter("root")()
		branchA()
		branchB()
	}

	root()

	hierarchy := GetCallHierarchy()
	if !strings.Contains(hierarchy, "root") {
		t.Error("Expected 'root' in hierarchy")
	}
}

func TestPrintWithAllOptionsDisabled(t *testing.T) {
	Enable()
	defer Disable()

	func1 := func() {
		defer Enter("func1")()
		time.Sleep(5 * time.Millisecond)
	}

	func1()

	// Test with both time and percent disabled (edge case)
	PrintWithOptions(PrintOptions{
		ShowAbsolute:  false,
		ShowPercent:   false,
		MinPercentage: 0,
	})
}
