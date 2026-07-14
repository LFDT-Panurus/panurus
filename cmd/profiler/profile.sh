#!/bin/bash
# profile.sh - Automatic profiling for Go benchmarks and tests
#
# Usage: ./profile.sh <test_or_benchmark_name> [options]
#
# Supports both benchmarks (BenchmarkXxx) and tests (TestXxx)
#
# Options:
#   -d, --display <mode>       Display mode: both, time, percent (default: both)
#   -f, --root-function <name> Start from this function (show subtree only)
#   -m, --min-percent <n>      Hide functions below this percentage (default: 0)
#   -o, --output <path>        Output file path (default: current directory)
#   -h, --help                 Show this help message

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

# Defaults
DISPLAY_MODE="both"
TEST_NAME=""
ROOT_FUNCTION=""
MIN_PERCENT="0"
OUTPUT_PATH=""
IS_BENCHMARK=false

# Paths
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RUN_DIR="$(pwd)"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -d|--display)
            DISPLAY_MODE="$2"
            shift 2
            ;;
        -f|--root-function)
            ROOT_FUNCTION="$2"
            shift 2
            ;;
        -m|--min-percent)
            MIN_PERCENT="$2"
            shift 2
            ;;
        -o|--output)
            OUTPUT_PATH="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 <test_or_benchmark_name> [options]"
            echo ""
            echo "Supports both benchmarks (BenchmarkXxx) and tests (TestXxx)"
            echo ""
            echo "Options:"
            echo "  -d, --display <mode>      Display mode: both, time, percent (default: both)"
            echo "  -f, --root-function <name> Start from this function (show subtree)"
            echo "  -m, --min-percent <n>     Hide functions below this % (default: 0)"
            echo "  -o, --output <path>       Output file path (default: current directory)"
            echo "  -h, --help                Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 BenchmarkValidatorTransfer"
            echo "  $0 TestValidatorTransfer"
            echo "  $0 BenchmarkValidatorTransfer -f VerifyTransfer -m 1.0"
            exit 0
            ;;
        *)
            if [[ -z "$TEST_NAME" ]]; then
                TEST_NAME="$1"
            else
                echo -e "${RED}Error: Unknown argument '$1'${NC}"
                exit 1
            fi
            shift
            ;;
    esac
done

# Validate
if [[ -z "$TEST_NAME" ]]; then
    echo -e "${RED}Error: Test or benchmark name is required${NC}"
    echo "Usage: $0 <test_or_benchmark_name> [options]"
    exit 1
fi

if [[ ! "$DISPLAY_MODE" =~ ^(both|time|percent)$ ]]; then
    echo -e "${RED}Error: Invalid display mode '$DISPLAY_MODE'. Must be: both, time, or percent${NC}"
    exit 1
fi

# Find test or benchmark
echo -e "${BLUE}Searching for: $TEST_NAME${NC}"
TEST_FILE=$(find "$REPO_ROOT/token" -name "*_test.go" -exec grep -l "func $TEST_NAME" {} \; | head -1)

if [[ -z "$TEST_FILE" ]]; then
    echo -e "${RED}Error: Test/Benchmark '$TEST_NAME' not found${NC}"
    exit 1
fi

TEST_DIR=$(dirname "$TEST_FILE")
echo -e "${GREEN}Found: $TEST_FILE${NC}"

# Auto-detect if it's a benchmark or test
if [[ "$TEST_NAME" == Benchmark* ]]; then
    IS_BENCHMARK=true
    echo -e "${BLUE}Type: Benchmark${NC}"
else
    IS_BENCHMARK=false
    echo -e "${BLUE}Type: Test${NC}"
fi

# Create temporary workspace
TEMP_DIR="/tmp/profiler-$$"
echo -e "${BLUE}Creating temporary workspace: $TEMP_DIR${NC}"
mkdir -p "$TEMP_DIR"

# Copy repository
echo -e "${BLUE}Copying repository...${NC}"
if command -v rsync > /dev/null 2>&1; then
    rsync -a --exclude='.git' "$REPO_ROOT/" "$TEMP_DIR/" > /dev/null 2>&1
else
    cp -R "$REPO_ROOT/." "$TEMP_DIR/" > /dev/null 2>&1
    rm -rf "$TEMP_DIR/.git" > /dev/null 2>&1
fi

WORK_DIR="$TEMP_DIR"
echo -e "${GREEN}Working in isolated copy${NC}"

# Inject tracer into test/benchmark
echo -e "${BLUE}Injecting tracer...${NC}"
TEMP_TEST_FILE="$WORK_DIR/${TEST_FILE#$REPO_ROOT/}"
TEMP_TEST_DIR=$(dirname "$TEMP_TEST_FILE")

cd "$SCRIPT_DIR"
go run inject-tracer.go -file "$TEMP_TEST_FILE" -bench "$TEST_NAME"
if [[ $? -ne 0 ]]; then
    echo -e "${RED}Error: Failed to inject tracer${NC}"
    exit 1
fi
echo -e "${GREEN}Tracer injected${NC}"

# Ensure tracer package is available in the temporary workspace
echo -e "${BLUE}Configuring tracer access...${NC}"
cd "$TEMP_TEST_DIR"

# Create a temporary go.mod for the tracer package (needed for replace directive)
TEMP_TRACER_DIR="$WORK_DIR/tools/profiler/tracer"
cat > "$TEMP_TRACER_DIR/go.mod" << EOF
module github.com/LFDT-Panurus/panurus/tools/profiler/tracer

go 1.24
EOF

# Add replace directive to point to the tracer in the temp workspace
go mod edit -replace github.com/LFDT-Panurus/panurus/tools/profiler/tracer="$TEMP_TRACER_DIR"
if ! go mod tidy 2>&1; then
    echo -e "${RED}Error: go mod tidy failed${NC}"
    exit 1
fi
echo -e "${GREEN}Tracer configured${NC}"

# Cleanup function
cleanup() {
    if [[ -d "$TEMP_DIR" ]]; then
        rm -rf "$TEMP_DIR"
        echo -e "${GREEN}Cleaned up temporary workspace${NC}"
    fi
}
trap cleanup EXIT INT TERM

# Instrument all packages
echo ""
echo -e "${BLUE}=== Instrumenting Packages ===${NC}"
cd "$SCRIPT_DIR"

echo -n "Counting packages... "
TOTAL_PACKAGES=0
while IFS= read -r -d '' dir; do
    if [[ "$dir" == *"/vendor/"* ]] || [[ "$dir" == *"/.git/"* ]]; then
        continue
    fi
    if [[ -n "$(find "$dir" -maxdepth 1 -name "*.go" -not -name "*_test.go" 2>/dev/null)" ]]; then
        TOTAL_PACKAGES=$((TOTAL_PACKAGES + 1))
    fi
done < <(find "$WORK_DIR/token" -type d -print0 2>/dev/null)
echo "$TOTAL_PACKAGES packages found"

PACKAGE_COUNT=0
echo -n "Progress: ["
PROGRESS_WIDTH=50
while IFS= read -r -d '' dir; do
    if [[ "$dir" == *"/vendor/"* ]] || [[ "$dir" == *"/.git/"* ]]; then
        continue
    fi
    
    if [[ -n "$(find "$dir" -maxdepth 1 -name "*.go" -not -name "*_test.go" 2>/dev/null)" ]]; then
        go run auto-instrument.go -dir "$dir" > /dev/null 2>&1 || true
        PACKAGE_COUNT=$((PACKAGE_COUNT + 1))
        
        PERCENT=$((PACKAGE_COUNT * 100 / TOTAL_PACKAGES))
        FILLED=$((PACKAGE_COUNT * PROGRESS_WIDTH / TOTAL_PACKAGES))
        printf "\rProgress: ["
        for ((i=0; i<FILLED; i++)); do printf "="; done
        for ((i=FILLED; i<PROGRESS_WIDTH; i++)); do printf " "; done
        printf "] %3d%% (%d/%d)" "$PERCENT" "$PACKAGE_COUNT" "$TOTAL_PACKAGES"
    fi
done < <(find "$WORK_DIR/token" -type d -print0 2>/dev/null)

echo ""
echo -e "${GREEN}Instrumented $PACKAGE_COUNT packages${NC}"

# Run test or benchmark
echo ""
if [[ "$IS_BENCHMARK" == true ]]; then
    echo -e "${BLUE}=== Running Benchmark ===${NC}"
else
    echo -e "${BLUE}=== Running Test ===${NC}"
fi
cd "$TEMP_TEST_DIR"

# Set display options
SHOW_TIME="true"
SHOW_PERCENT="true"

case $DISPLAY_MODE in
    time)
        SHOW_PERCENT="false"
        ;;
    percent)
        SHOW_TIME="false"
        ;;
esac

# Build arguments
BENCH_ARGS=""

if [[ "$SHOW_TIME" == "false" ]]; then
    BENCH_ARGS="$BENCH_ARGS -show-time=false"
fi

if [[ "$SHOW_PERCENT" == "false" ]]; then
    BENCH_ARGS="$BENCH_ARGS -show-percent=false"
fi

if [[ -n "$ROOT_FUNCTION" ]]; then
    BENCH_ARGS="$BENCH_ARGS -root-function=\"$ROOT_FUNCTION\""
fi

BENCH_ARGS="$BENCH_ARGS -min-percent=$MIN_PERCENT"

# Determine output file (default: directory where script was run)
if [[ -n "$OUTPUT_PATH" ]]; then
    OUTPUT_FILE="$OUTPUT_PATH"
else
    OUTPUT_FILE="$RUN_DIR/${TEST_NAME}_profile.md"
fi

echo ""
if [[ "$IS_BENCHMARK" == true ]]; then
    echo "Command: cd $TEMP_TEST_DIR && go test -run=^$ -bench=\"^${TEST_NAME}$\" -benchtime=1x"
else
    echo "Command: cd $TEMP_TEST_DIR && go test -run=\"^${TEST_NAME}$\" -v"
fi
echo "Output file: $OUTPUT_FILE"
echo "Output directory: $RUN_DIR"
echo ""

# Run and filter output (keep only profiling sections)
if [[ "$IS_BENCHMARK" == true ]]; then
    # Run benchmark
    if [[ -n "$BENCH_ARGS" ]]; then
        eval "go test -run=^$ -bench=\"^${TEST_NAME}$\" -benchtime=1x -args $BENCH_ARGS" 2>&1 | \
            sed 's/\x1b\[[0-9;]*m//g' | \
            awk '/^## Call Hierarchy/,/^PASS$/ {if (/^PASS$/) exit; print}' | \
            tee "$OUTPUT_FILE"
    else
        go test -run=^$ -bench="^${TEST_NAME}$" -benchtime=1x 2>&1 | \
            sed 's/\x1b\[[0-9;]*m//g' | \
            awk '/^## Call Hierarchy/,/^PASS$/ {if (/^PASS$/) exit; print}' | \
            tee "$OUTPUT_FILE"
    fi
else
    # Run regular test
    if [[ -n "$BENCH_ARGS" ]]; then
        eval "go test -run=\"^${TEST_NAME}$\" -v -args $BENCH_ARGS" 2>&1 | \
            sed 's/\x1b\[[0-9;]*m//g' | \
            awk '/^## Call Hierarchy/,/^PASS$/ {if (/^PASS$/) exit; print}' | \
            tee "$OUTPUT_FILE"
    else
        go test -run="^${TEST_NAME}$" -v 2>&1 | \
            sed 's/\x1b\[[0-9;]*m//g' | \
            awk '/^## Call Hierarchy/,/^PASS$/ {if (/^PASS$/) exit; print}' | \
            tee "$OUTPUT_FILE"
    fi
fi

echo ""
echo -e "${GREEN}=== Complete ===${NC}"
echo -e "${BLUE}Results: $OUTPUT_FILE${NC}"
