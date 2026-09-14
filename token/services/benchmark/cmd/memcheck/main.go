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
	"slices"
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

	// --- SECTION 2: HOT LINES ---
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

	// --- SECTION 3: LABELS ---
	if len(labels) > 0 {
		writef(w, "\n## 3. BUSINESS LOGIC CONTEXT (Labels)\n")
		writeLine(w, "LABEL\tALLOC %\tALLOC BYTES\tIN-USE BYTES")
		writeLine(w, "-----\t-------\t-----------\t------------")
		for i := 0; i < 10 && i < len(labels); i++ {
			l := labels[i]
			ratio := float64(l.AllocBytes) / float64(totalAlloc)
			writef(w, "%s\t%.1f%%\t%s\t%s\n", l.Name, ratio*100, formatBytes(l.AllocBytes), formatBytes(l.InUseBytes))
		}
	}

	// --- SECTION 4: TOP ALLOCATORS ---
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

	// --- SECTION 5: LEAKS ---
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

	// --- SECTION 6: TRACES ---
	if len(stats) > 0 {
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

	// --- SECTION 7: ASCII FLAME GRAPH ---
	writef(w, "\n## 7. ASCII FLAME GRAPH (Call Tree)\n")
	writef(w, " Showing paths consuming >1%% of total memory.\n\n")
	printFlameGraph(w, stacks, totalAlloc)

	if err := w.Flush(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "benchmark: flush error:", err)
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

		for _, fnName := range slices.Backward(rec.Stack) {

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

func detectAntiPatterns(stats []*FuncStat, w *tabwriter.Writer) {
	writef(w, "## 1. DETECTED ANTI-PATTERNS & HEURISTICS\n")
	writeLine(w, "FUNCTION\tISSUE\tADVICE")
	writeLine(w, "--------\t-----\t------")

	found := false
	for _, s := range stats {
		name := strings.ToLower(s.Name)

		if strings.Contains(name, "time.after") {
			writef(w, "%s\tLoop Timer Leak\tUse time.NewTicker or time.Timer + Stop()\n", shortenName(s.Name))
			found = true
		}
		if strings.Contains(name, "regexp.compile") && s.FlatAllocObj > 50 {
			writef(w, "%s\tRepeated RegEx\tCompile once in global var or init()\n", shortenName(s.Name))
			found = true
		}
		if strings.Contains(name, "json.unmarshal") && s.FlatAllocBytes > 1024*1024*10 {
			writef(w, "%s\tHeavy JSON\tUse json.Decoder or easyjson\n", shortenName(s.Name))
			found = true
		}
		if (strings.Contains(name, "slicebytetostring") || strings.Contains(name, "stringtoslicebyte")) && s.FlatAllocBytes > 1024*1024 {
			writef(w, "%s\tType Conv (Safe)\tHeavy []byte <-> string.\n", shortenName(s.Name))
			found = true
		}
		if strings.Contains(name, "runtime.convt") && s.FlatAllocBytes > 1024*1024 {
			writef(w, "%s\tInterface Boxing\tConcrete -> interface{}. Generics?\n", shortenName(s.Name))
			found = true
		}
		if strings.Contains(name, "growslice") && s.FlatAllocBytes > 1024*1024 {
			writef(w, "%s\tSlice Append\tPre-allocate: make([], 0, cap)\n", shortenName(s.Name))
			found = true
		}
		if (strings.Contains(name, "mapassign") || strings.Contains(name, "evacuate")) && s.FlatAllocBytes > 1024*1024 {
			writef(w, "%s\tMap Growth\tPre-allocate: make(map, cap)\n", shortenName(s.Name))
			found = true
		}
		if strings.Contains(name, "runtime.malg") && s.FlatAllocObj > 1000 {
			writef(w, "%s\tGoroutine Churn\tStarting %d+ goroutines. Worker Pool?\n", shortenName(s.Name), s.FlatAllocObj)
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
		writeLine(w, " (No trace found)")

		return
	}

	trace := stackDefinitions[maxSig]
	writeLine(w, " Trace (Leaf -> Root):")
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

	// 1. Identify Metrics
	idxAllocSpace, idxAllocObj := -1, -1
	idxInUseSpace, idxInUseObj := -1, -1

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

	if idxAllocSpace == -1 {
		log.Fatal("Profile missing 'alloc_space'. Ensure this is a heap profile.")
	}

	// 2. Aggregate Data
	stats := make(map[string]*FuncStat)
	lineStats := make(map[string]*LineStat)
	labelStats := make(map[string]*LabelStat)
	var totalAllocBytes, totalInUseBytes int64
	var topStacks []StackRecord

	for _, s := range p.Sample {
		allocBytes := s.Value[idxAllocSpace]
		allocObj := s.Value[idxAllocObj]
		inUseBytes := s.Value[idxInUseSpace]
		inUseObj := s.Value[idxInUseObj]

		totalAllocBytes += allocBytes
		totalInUseBytes += inUseBytes

		// A. Function Analysis
		seen := make(map[string]bool)
		if len(s.Location) > 0 {
			leafLoc := s.Location[0]
			if len(leafLoc.Line) > 0 {
				fn := leafLoc.Line[0].Function
				lineNo := int(leafLoc.Line[0].Line)

				if fn != nil {
					key := funcKey(fn)
					if _, ok := stats[key]; !ok {
						stats[key] = NewFuncStat(fn.Name, fn.Filename, lineNo)
					}
					stats[key].FlatAllocBytes += allocBytes
					stats[key].FlatAllocObj += allocObj
					stats[key].FlatInUseBytes += inUseBytes
					stats[key].FlatInUseObj += inUseObj

					if len(s.Location) > 1 {
						parentLoc := s.Location[1]
						if len(parentLoc.Line) > 0 {
							pFn := parentLoc.Line[0].Function
							if pFn != nil {
								stats[key].Callers[pFn.Name] += allocBytes
							}
						}
					}

					// Line Stat
					lineKey := fmt.Sprintf("%s:%d", fn.Filename, lineNo)
					if _, ok := lineStats[lineKey]; !ok {
						lineStats[lineKey] = &LineStat{File: fn.Filename, Line: lineNo, Function: fn.Name}
					}
					lineStats[lineKey].AllocBytes += allocBytes
				}
			}
		}

		// B. Stack Trace Collection (Cumulative & Tree)
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

				if _, ok := stats[key]; !ok {
					stats[key] = NewFuncStat(fn.Name, fn.Filename, int(line.Line))
				}
				stats[key].CumAllocBytes += allocBytes
			}
		}

		// C. Label Analysis
		for key, values := range s.Label {
			for _, val := range values {
				labelID := fmt.Sprintf("%s:%s", key, val)
				if _, ok := labelStats[labelID]; !ok {
					labelStats[labelID] = &LabelStat{Name: labelID}
				}
				labelStats[labelID].AllocBytes += allocBytes
				labelStats[labelID].InUseBytes += inUseBytes
			}
		}

		if allocBytes > 0 {
			topStacks = append(topStacks, StackRecord{Stack: currentStack, Bytes: allocBytes})
		}
	}

	// 3. Sorting
	var statList []*FuncStat
	for _, s := range stats {
		statList = append(statList, s)
	}
	var labelList []*LabelStat
	for _, s := range labelStats {
		labelList = append(labelList, s)
	}
	sort.Slice(labelList, func(i, j int) bool { return labelList[i].AllocBytes > labelList[j].AllocBytes })
	var lineList []*LineStat
	for _, s := range lineStats {
		lineList = append(lineList, s)
	}
	sort.Slice(lineList, func(i, j int) bool { return lineList[i].AllocBytes > lineList[j].AllocBytes })

	// 5. Generate Report
	printUnifiedReport(statList, labelList, lineList, topStacks, totalAllocBytes, totalInUseBytes)
}
