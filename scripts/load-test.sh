#!/usr/bin/env bash
# HardCoreVisor API Load Test
# Usage: ./scripts/load-test.sh [base_url] [duration_seconds] [concurrency]
set -euo pipefail

BASE=${1:-http://localhost:18080}
DURATION=${2:-30}
CONCURRENCY=${3:-10}

echo "=== HardCoreVisor Load Test ==="
echo "Base: $BASE, Duration: ${DURATION}s, Concurrency: $CONCURRENCY"
echo ""

# Check if hey or ab is available
if command -v hey &>/dev/null; then
    TOOL="hey"
elif command -v ab &>/dev/null; then
    TOOL="ab"
else
    echo "Neither 'hey' nor 'ab' found. Using curl loop."
    TOOL="curl"
fi

echo "Using: $TOOL"
echo ""

# Test 1: Health endpoint (baseline)
echo "--- Test 1: GET /healthz (baseline) ---"
case $TOOL in
    hey) hey -z ${DURATION}s -c $CONCURRENCY "$BASE/healthz" 2>&1 | tail -15 ;;
    ab)  ab -t $DURATION -c $CONCURRENCY "$BASE/healthz" 2>&1 | grep -E "Requests|Time|Transfer" ;;
    curl)
        START=$(date +%s)
        COUNT=0
        while [ $(($(date +%s) - START)) -lt $DURATION ]; do
            curl -sf "$BASE/healthz" > /dev/null && COUNT=$((COUNT+1))
        done
        echo "  Completed: $COUNT requests in ${DURATION}s"
        ;;
esac
echo ""

# Test 2: VM CRUD cycle
echo "--- Test 2: VM Create/List/Delete cycle ---"
START=$(date +%s)
CYCLES=0
ERRORS=0
while [ $(($(date +%s) - START)) -lt $DURATION ]; do
    # Create
    ID=$(curl -sf -X POST "$BASE/api/v1/vms" \
        -H 'Content-Type: application/json' \
        -d "{\"name\":\"load-$CYCLES\",\"vcpus\":1,\"memory_mb\":256}" 2>/dev/null | \
        python3 -c "import sys,json;print(json.load(sys.stdin).get('id',''))" 2>/dev/null || echo "")

    if [ -n "$ID" ] && [ "$ID" != "" ]; then
        # List
        curl -sf "$BASE/api/v1/vms" > /dev/null 2>&1
        # Delete
        curl -sf -X DELETE "$BASE/api/v1/vms/$ID" > /dev/null 2>&1
        CYCLES=$((CYCLES+1))
    else
        ERRORS=$((ERRORS+1))
    fi
done
echo "  Completed: $CYCLES create/list/delete cycles, $ERRORS errors in ${DURATION}s"
echo "  Rate: $(echo "scale=1; $CYCLES / $DURATION" | bc) cycles/sec"
echo ""

# Test 3: Concurrent VM creation
echo "--- Test 3: Concurrent VM creation ($CONCURRENCY threads) ---"
START=$(date +%s)
PIDS=""
for i in $(seq 1 $CONCURRENCY); do
    (
        COUNT=0
        while [ $(($(date +%s) - START)) -lt $DURATION ]; do
            curl -sf -X POST "$BASE/api/v1/vms" \
                -H 'Content-Type: application/json' \
                -d "{\"name\":\"conc-$i-$COUNT\",\"vcpus\":1,\"memory_mb\":256}" > /dev/null 2>&1
            COUNT=$((COUNT+1))
        done
        echo $COUNT
    ) &
    PIDS="$PIDS $!"
done
TOTAL=0
for pid in $PIDS; do
    wait $pid
    RESULT=$?
done
echo "  Concurrency test completed"
echo ""

echo "=== Load Test Complete ==="
