# Token Validation Service Benchmark

This document describes how to benchmark the Token Validation Service at the node level using the Fabric Smart Client (FSC) framework.

## Overview

The Token Validation Service benchmarks measure the performance of token request validation (including transfers, issues, and auditing) through different architectural layers:

1. **Local View Benchmark** - Direct in-process validation (no network overhead)
2. **API Benchmark** - Validation through FSC's View API (local node)
3. **gRPC API Benchmark** - Validation through gRPC client-server architecture (network overhead included)
4. **Distributed Benchmark** - Two-node setup with separate client and server machines

These benchmarks complement the [core driver benchmarks](core/dlognogh/dlognogh.md) by testing the complete validation service layer including FSC integration, network communication, and node orchestration.

## Motivation

The Token Validation Service benchmarks serve several critical purposes:

### Performance Validation
- **End-to-End Metrics**: Measure complete token request validation pipeline including deserialization, ZK proof verification, signature checks, auditing validation, and metadata verification
- **Network Overhead**: Quantify the impact of gRPC communication and serialization on overall performance
- **Scalability Testing**: Evaluate performance under concurrent load with multiple CPU cores and gRPC connections

### Architecture Comparison
- **Baseline (Local)**: Establish performance ceiling without network or API overhead
- **API Layer**: Measure FSC View API overhead for local node operations
- **gRPC Layer**: Quantify network communication costs in distributed deployments
- **Real-World Simulation**: Test actual deployment scenarios with separate client/server nodes

### Optimization Guidance
- **Bottleneck Identification**: Determine whether performance is limited by cryptography, network, or service logic
- **Configuration Tuning**: Find optimal settings for CPU cores, gRPC connections, and GOGC values
- **Deployment Planning**: Inform decisions about node placement, network topology, and resource allocation

## Prerequisites

Panurus comes pre-equipped with test data containing cryptographic parameters and sample token transfers located at:

```
token/core/zkatdlog/nogh/v1/validator/regression/testdata/
```

This directory includes:
- Public parameters for zero-knowledge proofs (e.g., `32-BLS12_381_BBS_GURVY/params.txt`)
- Pre-generated token transfer test cases in subdirectories like `transfers_i2_o2/`

**Note**: The test data is already included in the repository. You only need to regenerate it if you want to create custom test cases with different parameters or token configurations. To regenerate test data on demand:

```bash
cd token/core/zkatdlog/nogh/v1/validator
go test -run TestRegression -v
```
- Sample token commitments and metadata

The test data is required by all benchmark types (Local, API, gRPC, and Distributed).

## Quickstart

### 1. Local View Benchmark

Tests token validation directly in-process without any network or API overhead. This establishes the performance baseline.

```bash
cd cmd/token_validation_service
GOGC=10000 go test -run ^$ -bench=BenchmarkLocalTokenValidation -benchtime=30s -count=5 -cpu=1,4,8,16,32,64
```

**Parameters:**
- `GOGC=10000`: Reduces garbage collection frequency for more stable measurements
- `-benchtime=30s`: Run each benchmark for 30 seconds
- `-count=5`: Repeat each benchmark 5 times for statistical significance
- `-cpu=1,4,8,16,32,64`: Test with different numbers of parallel goroutines

### 2. API Benchmark (Local Node)

Tests token validation through FSC's View API on a single node. Measures API overhead compared to direct validation.

```bash
cd cmd/token_validation_service
GC=10000 go test -bench=BenchmarkAPI -benchtime=30s
```

**What it does:**
- Spins up a temporary FSC node with View API
- Submits token validation requests through the API
- Measures end-to-end latency including API serialization

### 3. gRPC API Benchmark (Local)

Tests token validation through gRPC client-server architecture on localhost. Measures network serialization overhead.

```bash
cd cmd/token_validation_service
GOGC=10000 go test -bench=BenchmarkAPIGRPC -benchtime=30s -count=5 -cpu=32 -numConn=1,2,4,8
```

**Parameters:**
- `-numConn=1,2,4,8`: Test with different numbers of gRPC client connections

**What it does:**
- Starts FSC node with gRPC server
- Creates gRPC client(s) connecting to localhost
- Measures latency including gRPC marshaling/unmarshaling and network stack

### 4. Distributed Two-Node Benchmark

To setup AWS EC2 nodes See: [AWS Benchmark 2 Machines](../../../cmd/token_validation_service/aws_bench_2_machines.md)

For realistic deployment testing with separate client and server machines:

Architecture: 
1. Machine 1 is the server (FC Node)
2. Machine 2 is the client sending to the server and gathering the metrics

Setup: 
1. Add ssh pubkey of server to client `known_hosts` (If not already connected)  
2. Start the server:

```bash
cd cmd/token_validation_service
GOGC=10000 go run ./server/
```
Wait until you see the output:
```bash
Running fscnode test-node
```
[Note: We set GOGC to 10K but you don't have to]

3. In the client, copy the node data from the server  
We can use `Rsync` for this:
```bash
cd cmd/token_validation_service

rsync -avz <YourServerName>:/<fullpath>/panurus/cmd/token_validation_service/out/ ./out
```
Note: Make sure full path and server name are correct

4. Replace the IP (or full name DNS can resolve) to the Server IP in the client out folder:

```bash
sed 's#127.0.0.1#123.456.789#g' ./out/testdata/fsc/nodes/test-node.0/client-config.yaml -i
```
5. Start client and tee output to file

```bash
c-workloads=token-validation-service -cpu=1,2,4,8,16,32,48,64 -numConn=1,2,4,8 2>&1 | tee out.txt &
```

This setup is ideal for:
- Testing real network latency and bandwidth constraints
- Evaluating performance on production-like infrastructure
- Measuring impact of geographic distribution
- Load testing with dedicated client machines
## Visualising the results 

0. Set up python environment:

```bash
python -m venv .venv 
source .venv/bin/activate
pip install streamlit pandas
```

1. Place the results in `.txt` files in a folder called `bench`.

2. Run:
```bash
streamlit run cmd/benchmarking/plotly_plot_node.py
```

## Real world example:
- Server: dectrust2.vpc.cloud9.ibm.com
- Client: dectrust1.vpc.cloud9.ibm.com

They both already have each-others SSH Keys, so I don't need to do it again.

On dectrust2, Server Runs:
```bash
cd cmd/token_validation_service
GOGC=10000 go run ./server/
```
I wait to see:

```bash
Running fscnode test-node
```

On dectrust1:
```bash
cd ~/effi/panurus/cmd/token_validation_service
rsync -avz root@dectrust2.vpc.cloud9.ibm.com:/root/effi/panurus/cmd/token_validation_service/out/ ./out
sed 's#127.0.0.1#dectrust1.vpc.cloud9.ibm.com#g' ./out/testdata/fsc/nodes/test-node.0/client-config.yaml -i
GOGC=10000 nohup go run ./client/ -benchtime=30s -count=5 -workloads=zkp -cpu=1,2,4,8,16,32 -numConn=1,2,4,8 2>&1 | tee example-2node.txt &
```

I also run gRPC benchmark without the 2 node setup:

```bash
GOGC=10000 go test -bench=BenchmarkAPIGRPC -benchtime=30s -count=5 -cpu=32 -numConn=1,2,4,8 | tee example-grpc.txt
```

I now move the outputs to `bench` folder and run the visualisation:

```bash
mv example-2node.txt bench
mv example-grpc.txt
```

(Optional) I setup python `venv`:
```bash
python -m venv .venv 
source .venv/bin/activate
pip install streamlit pandas
```

Now I can run the visualisation:
```bash 
streamlit run cmd/benchmarking/plotly_plot_node.py 
```

## Understanding Results

### Metrics Reported

Each benchmark reports:
- **ns/op**: Nanoseconds per operation (lower is better)
- **TPS**: Transactions per second (higher is better)
- **B/op**: Bytes allocated per operation
- **allocs/op**: Number of allocations per operation

### Example Output

```
BenchmarkLocalTokenValidation/out-tokens=2in-tokens=2-32    1000    30000000 ns/op    33333 TPS
```

**Interpretation:**
- Test ran with 32 parallel goroutines (`-cpu=32`)
- 2 input tokens, 2 output tokens
- Each validation took ~30ms (30,000,000 ns)
- Achieved ~33,333 transactions per second


## Related Documentation

- **[Core Driver Benchmarks](core/dlognogh/dlognogh.md)** - Lower-level cryptographic benchmarks
- **[Testing Architecture](core/dlognogh/dlognogh_architecture.md)** - Understanding the test layers
- **[Benchmark Tools](tools.md)** - Analysis and profiling tools
- **[AWS Setup Guide](../../../cmd/token_validation_service/aws_bench_2_machines.md)** - Cloud deployment instructions

