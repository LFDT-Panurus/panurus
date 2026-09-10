/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/pprof/profile"
)

const (
	NoiseFloor = 0.005 // 0.5%
)

type FuncStat struct {
	Name           string
	File           string
	Line           int
	FlatAllocBytes int64
	FlatAllocObj   int64
	FlatInUseBytes int64
	FlatInUseObj   int64
	CumAllocBytes  int64
	Callers        map[string]int64
}

type LineStat struct {
	File       string
	Line       int
	Function   string
	AllocBytes int64
}

type LabelStat struct {
	Name       string
	AllocBytes int64
	InUseBytes int64
}

type StackRecord struct {
	Stack []string // Leaf -> Root
	Bytes int64
}

// FlameNode for the ASCII Tree
type FlameNode struct {
	Name     string
	Total    int64
	Children map[string]*FlameNode
}

func NewFuncStat(name, file string, line int) *FuncStat {
	return &FuncStat{
		Name:    name,
		File:    file,
		Line:    line,
		Callers: make(map[string]int64),
	}
}

func printUnifiedReport(stats []*FuncStat, labels []*LabelStat, lines []*LineStat, stacks []StackRecord, totalAlloc, totalInUse int64) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	writef(w, "\n================ ULTIMATE GO MEMORY ANALYZER ================\n")
	writef(w, "Total Allocated: %s | Total In-Use: %s\n\n", formatBytes(totalAlloc), formatBytes(totalInUse))

	detectAntiPatterns(stats, w)
	printHotLines(w, lines, totalAlloc)
	printLabelStats(w, labels, totalAlloc)
	printTopAllocators(w, stats, totalAlloc)
	printLeakCandidates(w, stats, totalInUse, totalAlloc)
	printRootCauseTrace(w, stats, stacks, totalAlloc)

	// --- SECTION 7: ASCII FLAME GRAPH ---
	writef(w, "\n## 7. ASCII FLAME GRAPH (Call Tree)\n")
	writef(w, " Showing paths consuming >1%% of total memory.\n\n")
	printFlameGraph(w, stacks, totalAlloc)

	if err := w.Flush(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "benchmark: flush error:", err)
	}
}

// printHotLines renders SECTION 2: the source lines with the highest allocation volume.
func printHotLines(w *tabwriter.Writer, lines []*LineStat, totalAlloc int64) {
	writef(w, "\n## 2. HOT LINES (Exact Source Location)\n")
	writeLine(w, "FILE:LINE\tFUNCTION\tALLOC BYTES\t% TOTAL")
	writeLine(w, "---------\t--------\t-----------\t-------")
	for i := 0; i < 10 && i < len(lines); i++ {
		l := lines[i]
		ratio := float64(l.AllocBytes) / float64(totalAlloc)
		if ratio < NoiseFloor {
			continue
		}
		writef(w, "%s:%d\t%s\t%s\t%.1f%%\n", shortenPath(l.File), l.Line, shortenName(l.Function), formatBytes(l.AllocBytes), ratio*100)
	}
}

// printLabelStats renders SECTION 3: allocation/in-use breakdown by pprof label.
func printLabelStats(w *tabwriter.Writer, labels []*LabelStat, totalAlloc int64) {
	if len(labels) == 0 {
		return
	}
	writef(w, "\n## 3. BUSINESS LOGIC CONTEXT (Labels)\n")
	writeLine(w, "LABEL\tALLOC %\tALLOC BYTES\tIN-USE BYTES")
	writeLine(w, "-----\t-------\t-----------\t------------")
	for i := 0; i < 10 && i < len(labels); i++ {
		l := labels[i]
		ratio := float64(l.AllocBytes) / float64(totalAlloc)
		writef(w, "%s\t%.1f%%\t%s\t%s\n", l.Name, ratio*100, formatBytes(l.AllocBytes), formatBytes(l.InUseBytes))
	}
}

// printTopAllocators renders SECTION 4: the functions producing the most objects.
func printTopAllocators(w *tabwriter.Writer, stats []*FuncStat, totalAlloc int64) {
	writef(w, "\n## 4. TOP OBJECT PRODUCERS (GC Pressure)\n")
	writeLine(w, "NAME\tFLAT %\tFLAT BYTES\tAVG SIZE\tIMMEDIATE CALLER")
	writeLine(w, "----\t------\t----------\t--------\t----------------")
	sort.Slice(stats, func(i, j int) bool { return stats[i].FlatAllocBytes > stats[j].FlatAllocBytes })
	for i := 0; i < 20 && i < len(stats); i++ {
		s := stats[i]
		ratio := float64(s.FlatAllocBytes) / float64(totalAlloc)
		if ratio < NoiseFloor {
			continue
		}
		avgSize := int64(0)
		if s.FlatAllocObj > 0 {
			avgSize = s.FlatAllocBytes / s.FlatAllocObj
		}
		writef(w, "%s\t%.1f%%\t%s\t%d B\t%s\n", shortenName(s.Name), ratio*100, formatBytes(s.FlatAllocBytes), avgSize, getTopCallers(s.Callers, s.FlatAllocBytes))
	}
}

// printLeakCandidates renders SECTION 5: functions retaining the most in-use memory.
func printLeakCandidates(w *tabwriter.Writer, stats []*FuncStat, totalInUse, totalAlloc int64) {
	writef(w, "\n## 5. PERSISTENT MEMORY (Leak Candidates)\n")
	writeLine(w, "NAME\tIN-USE %\tIN-USE BYTES\tALLOC %\tSUGGESTION/DIAGNOSIS")
	writeLine(w, "----\t--------\t------------\t-------\t--------------------")
	sort.Slice(stats, func(i, j int) bool { return stats[i].FlatInUseBytes > stats[j].FlatInUseBytes })
	for i := 0; i < 15 && i < len(stats); i++ {
		s := stats[i]
		inUseRatio := float64(s.FlatInUseBytes) / float64(totalInUse)
		allocRatio := float64(s.FlatAllocBytes) / float64(totalAlloc)
		if inUseRatio < NoiseFloor {
			continue
		}
		writef(w, "%s\t%.1f%%\t%s\t%.1f%%\t%s\n", shortenName(s.Name), inUseRatio*100, formatBytes(s.FlatInUseBytes), allocRatio*100, suggestFix(s, inUseRatio, allocRatio))
	}
}

// printRootCauseTrace renders SECTION 6: the deepest allocation stack for the top offenders.
func printRootCauseTrace(w *tabwriter.Writer, stats []*FuncStat, stacks []StackRecord, totalAlloc int64) {
	if len(stats) == 0 {
		return
	}
	writef(w, "\n## 6. ROOT CAUSE TRACE (Top 5 Allocators)\n")
	sort.Slice(stats, func(i, j int) bool { return stats[i].FlatAllocBytes > stats[j].FlatAllocBytes })
	count := 0
	for i := 0; i < len(stats) && count < 5; i++ {
		s := stats[i]
		if float64(s.FlatAllocBytes)/float64(totalAlloc) < NoiseFloor {
			continue
		}
		writef(w, "\n [Rank #%d] Offender: %s\n", count+1, shortenName(s.Name))
		printHotStackWithBlame(w, s.Name, stacks)
		count++
	}
}

func printFlameGraph(w *tabwriter.Writer, stacks []StackRecord, totalAlloc int64) {
	// 1. Build Trie
	root := &FlameNode{Name: "Total", Total: 0, Children: make(map[string]*FlameNode)}

	for _, rec := range stacks {
		// rec.Stack is Leaf -> Root (e.g. [Malloc, FuncA, Main])
		// We need Root -> Leaf for the tree (e.g. Main -> FuncA -> Malloc)
		if len(rec.Stack) == 0 {
			continue
		}
		current := root
		root.Total += rec.Bytes

		for i := len(rec.Stack) - 1; i >= 0; i-- {
			fnName := rec.Stack[i]
			if _, exists := current.Children[fnName]; !exists {
				current.Children[fnName] = &FlameNode{
					Name:     fnName,
					Children: make(map[string]*FlameNode),
				}
			}
			current = current.Children[fnName]
			current.Total += rec.Bytes
		}
	}

	// 2. Print Trie
	printFlameNode(w, root, "", totalAlloc, true)
}

func printFlameNode(w *tabwriter.Writer, node *FlameNode, prefix string, totalAlloc int64, isLast bool) {
	// Cutoff: Hide nodes with < 1% impact
	ratio := float64(node.Total) / float64(totalAlloc)
	if ratio < 0.01 {
		return
	}

	// Prepare display
	connector := "├── "
	if isLast {
		connector = "└── "
	}
	if prefix == "" {
		connector = "" // Root
	}

	// Print Node
	name := shortenName(node.Name)
	if node.Name == "Total" {
		name = "TOTAL ALLOC"
	}
	writef(w, "%s%s%s (%s, %.1f%%)\n", prefix, connector, name, formatBytes(node.Total), ratio*100)

	// Prepare prefix for children
	childPrefix := prefix
	if prefix == "" {
		childPrefix = ""
	} else if isLast {
		childPrefix += "    "
	} else {
		childPrefix += "│   "
	}

	// Sort Children by Total Bytes (descending)
	type childSort struct {
		Name  string
		Total int64
	}
	var children []childSort
	for _, c := range node.Children {
		children = append(children, childSort{c.Name, c.Total})
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Total > children[j].Total })

	// Recursively print children
	for i, c := range children {
		childNode := node.Children[c.Name]
		printFlameNode(w, childNode, childPrefix, totalAlloc, i == len(children)-1)
	}
}

// --- Existing Heuristics & Helpers ---

// antiPatternRule describes one heuristic detectAntiPatterns checks a FuncStat
// against: a name substring match, plus an optional allocation-size/count threshold
// that must also be exceeded for the rule to fire.
type antiPatternRule struct {
	substrs        []string
	issue          string
	advice         string
	thresholdBytes int64
	thresholdObjs  int64
	dynamicAdvice  func(s *FuncStat) string
}

var antiPatternRules = []antiPatternRule{
	{substrs: []string{"time.after"}, issue: "Loop Timer Leak", advice: "Use time.NewTicker or time.Timer + Stop()"},
	{substrs: []string{"regexp.compile"}, issue: "Repeated RegEx", advice: "Compile once in global var or init()", thresholdObjs: 50},
	{substrs: []string{"json.unmarshal"}, issue: "Heavy JSON", advice: "Use json.Decoder or easyjson", thresholdBytes: 1024 * 1024 * 10},
	{substrs: []string{"slicebytetostring", "stringtoslicebyte"}, issue: "Type Conv (Safe)", advice: "Heavy []byte <-> string.", thresholdBytes: 1024 * 1024},
	{substrs: []string{"runtime.convt"}, issue: "Interface Boxing", advice: "Concrete -> interface{}. Generics?", thresholdBytes: 1024 * 1024},
	{substrs: []string{"growslice"}, issue: "Slice Append", advice: "Pre-allocate: make([], 0, cap)", thresholdBytes: 1024 * 1024},
	{substrs: []string{"mapassign", "evacuate"}, issue: "Map Growth", advice: "Pre-allocate: make(map, cap)", thresholdBytes: 1024 * 1024},
	{substrs: []string{"runtime.malg"}, issue: "Goroutine Churn", thresholdObjs: 1000, dynamicAdvice: func(s *FuncStat) string {
		return fmt.Sprintf("Starting %d+ goroutines. Worker Pool?", s.FlatAllocObj)
	}},
}

// matches reports whether s trips this rule: its lowercased name contains one of the
// rule's substrings and, if set, its allocation bytes/objects exceed the threshold.
func (r antiPatternRule) matches(name string, s *FuncStat) bool {
	matched := false
	for _, sub := range r.substrs {
		if strings.Contains(name, sub) {
			matched = true

			break
		}
	}
	if !matched {
		return false
	}
	if r.thresholdBytes > 0 && s.FlatAllocBytes <= r.thresholdBytes {
		return false
	}
	if r.thresholdObjs > 0 && s.FlatAllocObj <= r.thresholdObjs {
		return false
	}

	return true
}

func (r antiPatternRule) adviceFor(s *FuncStat) string {
	if r.dynamicAdvice != nil {
		return r.dynamicAdvice(s)
	}

	return r.advice
}

func detectAntiPatterns(stats []*FuncStat, w *tabwriter.Writer) {
	writef(w, "## 1. DETECTED ANTI-PATTERNS & HEURISTICS\n")
	writeLine(w, "FUNCTION\tISSUE\tADVICE")
	writeLine(w, "--------\t-----\t------")

	found := false
	for _, s := range stats {
		name := strings.ToLower(s.Name)
		for _, rule := range antiPatternRules {
			if !rule.matches(name, s) {
				continue
			}
			writef(w, "%s\t%s\t%s\n", shortenName(s.Name), rule.issue, rule.adviceFor(s))
			found = true
		}
	}
	if !found {
		writeLine(w, "None\t-\tNo obvious anti-patterns found.")
	}
}

func suggestFix(s *FuncStat, inUseRatio, allocRatio float64) string {
	name := strings.ToLower(s.Name)
	if strings.Contains(name, "buf") || strings.Contains(name, "read") {
		return "Buffer growth? Check capacity reset."
	}
	if strings.Contains(name, "cache") || strings.Contains(name, "map") {
		return "Unbounded Map/Cache? Add eviction."
	}
	if allocRatio < 0.001 {
		return "Static Data. Safe if expected."
	}
	if inUseRatio > 0.30 {
		return "CRITICAL: Holds >30% RAM."
	}

	return "Inspect retention logic."
}

func printHotStackWithBlame(w *tabwriter.Writer, targetFunc string, stacks []StackRecord) {
	trace := findHeaviestStackForTarget(targetFunc, stacks)
	if trace == nil {
		writeLine(w, " (No trace found)")

		return
	}

	writeLine(w, " Trace (Leaf -> Root):")
	printBlameTrace(w, trace)
}

// findHeaviestStackForTarget returns the highest-byte-sum stack (Leaf -> Root) among
// the stacks whose leaf is targetFunc, or nil if none match.
func findHeaviestStackForTarget(targetFunc string, stacks []StackRecord) []string {
	stackSums := make(map[string]int64)
	stackDefinitions := make(map[string][]string)

	for _, rec := range stacks {
		if len(rec.Stack) == 0 {
			continue
		}
		if rec.Stack[0] == targetFunc {
			sig := strings.Join(rec.Stack, ";")
			stackSums[sig] += rec.Bytes
			stackDefinitions[sig] = rec.Stack
		}
	}

	var maxSig string
	var maxBytes int64
	for sig, b := range stackSums {
		if b > maxBytes {
			maxBytes = b
			maxSig = sig
		}
	}

	if maxSig == "" {
		return nil
	}

	return stackDefinitions[maxSig]
}

// printBlameTrace prints trace (Leaf -> Root), marking the allocator at the leaf and
// the first non-stdlib frame as the likely entry point, truncating past 15 frames.
func printBlameTrace(w *tabwriter.Writer, trace []string) {
	blameFound := false
	for i, fn := range trace {
		indent := strings.Repeat(" ", i)
		marker := ""
		if !blameFound && !isStdLib(fn) {
			marker = "  <-- [LIKELY CAUSE / ENTRY POINT]"
			blameFound = true
		}
		if i == 0 {
			marker = "  (Allocator)"
		}
		writef(w, " %s-> %s%s\n", indent, shortenName(fn), marker)
		if i >= 15 {
			writef(w, " %s ...\n", indent)

			break
		}
	}
}

func isStdLib(funcName string) bool {
	prefixes := []string{
		"runtime", "sync", "syscall", "net", "io", "bufio", "bytes", "strings",
		"encoding", "time", "reflect", "math", "sort", "compress", "crypto",
		"internal", "os", "path", "fmt", "log",
	}
	clean := strings.TrimLeft(funcName, "*")
	for _, p := range prefixes {
		if strings.HasPrefix(clean, p+".") || strings.HasPrefix(clean, p+"/") {
			return true
		}
	}

	return false
}

func funcKey(fn *profile.Function) string {
	return fmt.Sprintf("%s:%s", fn.Name, fn.Filename)
}

func getTopCallers(callers map[string]int64, total int64) string {
	if len(callers) == 0 {
		return "[Root]"
	}
	type caller struct {
		Name  string
		Bytes int64
	}
	var list []caller
	for k, v := range callers {
		list = append(list, caller{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Bytes > list[j].Bytes })
	var parts []string
	for i := 0; i < 2 && i < len(list); i++ {
		pct := float64(list[i].Bytes) / float64(total) * 100
		parts = append(parts, fmt.Sprintf("%s (%.0f%%)", shortenName(list[i].Name), pct))
	}

	return strings.Join(parts, ", ")
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func shortenName(n string) string {
	parts := strings.Split(n, "/")

	return parts[len(parts)-1]
}

func shortenPath(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}

	return p
}

func writef(w *tabwriter.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func writeLine(w *tabwriter.Writer, s string) {
	_, _ = fmt.Fprintln(w, s)
}

func main() {
	p := loadProfile()

	idxAllocSpace, idxAllocObj, idxInUseSpace, idxInUseObj := findSampleTypeIndices(p)
	if idxAllocSpace == -1 {
		log.Fatal("Profile missing 'alloc_space'. Ensure this is a heap profile.")
	}

	data := aggregateSamples(p, idxAllocSpace, idxAllocObj, idxInUseSpace, idxInUseObj)
	statList, labelList, lineList := data.sortedReportInputs()

	// 5. Generate Report
	printUnifiedReport(statList, labelList, lineList, data.topStacks, data.totalAllocBytes, data.totalInUseBytes)
}

// loadProfile parses the pprof file named by the command's single positional argument,
// exiting the process via log.Fatal on any usage or parse error.
func loadProfile() *profile.Profile {
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatal("Usage: memcheck <pprof_file>")
	}

	filename := flag.Arg(0)
	f, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}
	defer func() {
		_ = f.Close()
	}()

	p, err := profile.Parse(f)
	if err != nil {
		log.Fatalf("Failed to parse profile: %v", err)
	}

	return p
}

// findSampleTypeIndices locates the sample-value indices for alloc/in-use bytes and
// object counts within the profile's declared sample types. -1 means not found.
func findSampleTypeIndices(p *profile.Profile) (idxAllocSpace, idxAllocObj, idxInUseSpace, idxInUseObj int) {
	idxAllocSpace, idxAllocObj = -1, -1
	idxInUseSpace, idxInUseObj = -1, -1

	for i, st := range p.SampleType {
		switch st.Type {
		case "alloc_space", "alloc_bytes":
			idxAllocSpace = i
		case "alloc_objects", "alloc_count":
			idxAllocObj = i
		case "inuse_space", "inuse_bytes":
			idxInUseSpace = i
		case "inuse_objects", "inuse_count":
			idxInUseObj = i
		}
	}

	return idxAllocSpace, idxAllocObj, idxInUseSpace, idxInUseObj
}

// aggregatedData accumulates per-function, per-line and per-label statistics, plus
// totals and the raw stacks, across every sample in a profile.
type aggregatedData struct {
	stats           map[string]*FuncStat
	lineStats       map[string]*LineStat
	labelStats      map[string]*LabelStat
	totalAllocBytes int64
	totalInUseBytes int64
	topStacks       []StackRecord
}

// aggregateSamples walks every sample in p and folds it into a fresh aggregatedData.
func aggregateSamples(p *profile.Profile, idxAllocSpace, idxAllocObj, idxInUseSpace, idxInUseObj int) *aggregatedData {
	data := &aggregatedData{
		stats:      make(map[string]*FuncStat),
		lineStats:  make(map[string]*LineStat),
		labelStats: make(map[string]*LabelStat),
	}

	for _, s := range p.Sample {
		data.addSample(s, s.Value[idxAllocSpace], s.Value[idxAllocObj], s.Value[idxInUseSpace], s.Value[idxInUseObj])
	}

	return data
}

// addSample folds one profile sample into the aggregate, covering function-level
// flat stats (A), cumulative stack-trace stats (B), and label stats (C).
func (d *aggregatedData) addSample(s *profile.Sample, allocBytes, allocObj, inUseBytes, inUseObj int64) {
	d.totalAllocBytes += allocBytes
	d.totalInUseBytes += inUseBytes

	d.recordFunctionStat(s, allocBytes, allocObj, inUseBytes, inUseObj)
	currentStack := d.recordStackTrace(s, allocBytes)
	d.recordLabelStats(s, allocBytes, inUseBytes)

	if allocBytes > 0 {
		d.topStacks = append(d.topStacks, StackRecord{Stack: currentStack, Bytes: allocBytes})
	}
}

// recordFunctionStat updates the flat allocation/in-use stats and the immediate-caller
// and hot-line breakdowns for the sample's leaf function (SECTION A).
func (d *aggregatedData) recordFunctionStat(s *profile.Sample, allocBytes, allocObj, inUseBytes, inUseObj int64) {
	if len(s.Location) == 0 {
		return
	}
	leafLoc := s.Location[0]
	if len(leafLoc.Line) == 0 {
		return
	}
	fn := leafLoc.Line[0].Function
	lineNo := int(leafLoc.Line[0].Line)
	if fn == nil {
		return
	}

	key := funcKey(fn)
	if _, ok := d.stats[key]; !ok {
		d.stats[key] = NewFuncStat(fn.Name, fn.Filename, lineNo)
	}
	d.stats[key].FlatAllocBytes += allocBytes
	d.stats[key].FlatAllocObj += allocObj
	d.stats[key].FlatInUseBytes += inUseBytes
	d.stats[key].FlatInUseObj += inUseObj

	if len(s.Location) > 1 {
		parentLoc := s.Location[1]
		if len(parentLoc.Line) > 0 {
			pFn := parentLoc.Line[0].Function
			if pFn != nil {
				d.stats[key].Callers[pFn.Name] += allocBytes
			}
		}
	}

	lineKey := fmt.Sprintf("%s:%d", fn.Filename, lineNo)
	if _, ok := d.lineStats[lineKey]; !ok {
		d.lineStats[lineKey] = &LineStat{File: fn.Filename, Line: lineNo, Function: fn.Name}
	}
	d.lineStats[lineKey].AllocBytes += allocBytes
}

// recordStackTrace walks the sample's full location stack, updating cumulative
// allocation stats for every function seen and returning the Leaf->Root name stack
// (SECTION B).
func (d *aggregatedData) recordStackTrace(s *profile.Sample, allocBytes int64) []string {
	seen := make(map[string]bool)
	var currentStack []string

	for _, loc := range s.Location {
		for _, line := range loc.Line {
			fn := line.Function
			if fn == nil {
				continue
			}
			currentStack = append(currentStack, fn.Name)

			key := funcKey(fn)
			if seen[key] {
				continue
			}
			seen[key] = true

			if _, ok := d.stats[key]; !ok {
				d.stats[key] = NewFuncStat(fn.Name, fn.Filename, int(line.Line))
			}
			d.stats[key].CumAllocBytes += allocBytes
		}
	}

	return currentStack
}

// recordLabelStats folds the sample's pprof labels into per-label allocation and
// in-use totals (SECTION C).
func (d *aggregatedData) recordLabelStats(s *profile.Sample, allocBytes, inUseBytes int64) {
	for key, values := range s.Label {
		for _, val := range values {
			labelID := fmt.Sprintf("%s:%s", key, val)
			if _, ok := d.labelStats[labelID]; !ok {
				d.labelStats[labelID] = &LabelStat{Name: labelID}
			}
			d.labelStats[labelID].AllocBytes += allocBytes
			d.labelStats[labelID].InUseBytes += inUseBytes
		}
	}
}

// sortedReportInputs flattens and sorts the aggregate into the slices printUnifiedReport
// expects: functions in map order, labels and lines sorted by allocation bytes descending.
func (d *aggregatedData) sortedReportInputs() (statList []*FuncStat, labelList []*LabelStat, lineList []*LineStat) {
	for _, s := range d.stats {
		statList = append(statList, s)
	}
	for _, s := range d.labelStats {
		labelList = append(labelList, s)
	}
	sort.Slice(labelList, func(i, j int) bool { return labelList[i].AllocBytes > labelList[j].AllocBytes })
	for _, s := range d.lineStats {
		lineList = append(lineList, s)
	}
	sort.Slice(lineList, func(i, j int) bool { return lineList[i].AllocBytes > lineList[j].AllocBytes })

	return statList, labelList, lineList
}
