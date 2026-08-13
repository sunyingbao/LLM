# NCU Complete Reference Guide

## Table of Contents
- [Installation and Requirements](#installation-and-requirements)
- [Command-Line Options](#command-line-options)
- [Output Sections](#output-sections)
- [Performance Metrics](#performance-metrics)
- [Configuration Files](#configuration-files)
- [Troubleshooting](#troubleshooting)

## Installation and Requirements

### Prerequisites
- NVIDIA GPU (Pascal, Volta, Turing, Ampere, Hopper, or later)
- CUDA Toolkit
- Latest NVIDIA driver
- Nsight Compute (contains ncu)

### Check Installation
```bash
ncu --version
```

### Install Nsight Compute
Download from: https://developer.nvidia.com/nsight-compute

## Command-Line Options

### Basic Options
```
--mode=launch        - Launch application and profile
--mode=attach        - Attach to running process
-k <kernel_name>     - Profile specific kernel
-o <output>          - Output report file
--set <preset>       - Use preset configuration (basic, full, etc.)
```

### Output Format Options
```
--export csv         - Export to CSV format
--export json        - Export to JSON format
--export sqlite      - Export to SQLite database
--import <file>      - Import existing report
```

### Filtering Options
```
--kernel-name-base   - Filter by kernel name pattern
--kernel-name-regex  - Regex filter for kernel names
--source-line        - Profile specific source line
--demangle           - Demangle C++ names (default: true)
```

### Profiling Control
```
--page-size <size>   - Set page size for range profiling
--sample <mode>      - Sampling mode
--target-processes   - Target specific processes
--kill <option>      - Process termination option (yes, no, never)
```

## Output Sections

### Available Sections
- **SpeedOfLight** - Hardware limits comparison
- **MemoryWorkloadAnalysis** - Memory access patterns
- **LaunchStatistics** - Kernel launch statistics
- **SourceCounters** - Per-source-line metrics
- **InstructionStats** - Instruction execution statistics
- **WarpState** - Warp-level state information
- **WorkloadAnalysis** - Thread and block workload distribution

### Using Sections
```bash
# Single section
ncu --section SpeedOfLight ./program

# Multiple sections
ncu --section SpeedOfLight,MemoryWorkloadAnalysis ./program
```

## Performance Metrics

### Memory Metrics
```
dram__throughput.avg.pct_of_peak           - DRAM throughput % of peak
dram__throughput.avg.pct_of_peak.sustained - Sustained DRAM throughput
lts__throughput.avg.pct_of_peak             - L2 cache throughput % of peak
lts__throughput.avg.pct_of_peak.sustained   - Sustained L2 throughput
```

### Compute Metrics
```
smsp__sass_thread_inst_executed_op_fadd_pred_on.sum.per_second_active - FADD ops/sec
smsp__sass_thread_inst_executed_op_fmul_pred_on.sum.per_second_active - FMUL ops/sec
smsp__sass_thread_inst_executed_op_ffma_pred_on.sum.per_second_active - FFMA ops/sec
smsp__sass_thread_inst_executed_op_integer_pred_on.sum.per_second_active - Integer ops/sec
```

### Occupancy Metrics
```
smsp__cycles_active.avg.pct_of_peak_sustained_elapsed              - SM active cycles %
smsp__warps_active.avg.per_cycle_active                             - Active warps per cycle
smsp__threads_launched.sum.per_cycle_active                         - Threads launched per cycle
```

### Warp Execution Metrics
```
smsp__pipe_alu_cycles_active.avg.pct_of_peak                        - ALU pipeline utilization
smsp__pipe_fp_cycles_active.avg.pct_of_peak                         - FP pipeline utilization
smsp__pipe_ldst_cycles_active.avg.pct_of_peak                       - Load/store pipeline utilization
```

### Instruction Mix Metrics
```
smsp__sass_thread_inst_executed_op_fp_pred_on.sum.pct_of_total_sa_inst_executed - FP ops %
smsp__sass_thread_inst_executed_op_integer_pred_on.sum.pct_of_total_sa_inst_executed - Integer ops %
smsp__sass_thread_inst_executed_op_ld_pred_on.sum.pct_of_total_sa_inst_executed - Load ops %
smsp__sass_thread_inst_executed_op_st_pred_on.sum.pct_of_total_sa_inst_executed - Store ops %
```

## Configuration Files

### Custom Config File Format
```ini
[configuration]
mode=launch
target-processes=all

[metrics]
collect=dram__throughput.avg.pct_of_peak
collect=smsp__sass_thread_inst_executed_op_fadd_pred_on.sum

[sections]
include=SpeedOfLight
include=MemoryWorkloadAnalysis
```

### Using Custom Config
```bash
ncu --config my_config.ini ./program
```

### Preset Configurations
```bash
# Basic profiling (minimal overhead)
ncu --set basic ./program

# Standard profiling (balanced)
ncu --set normal ./program

# Detailed profiling (maximum metrics)
ncu --set full ./program

# Memory-focused profiling
ncu --set memory ./program

# Compute-focused profiling
ncu --set compute ./program
```

## Troubleshooting

### Permission Issues
```bash
# Try with sudo
sudo ncu ./program

# Check permissions
ls -l /dev/nvidia*
```

### GPU Architecture Support
```bash
# Check GPU architecture
nvidia-smi --query-gpu=compute_cap --format=csv
```

### Profiling Overhead
Expected overhead: 2-10x slower than normal execution. This is normal and expected.

### Incomplete Results
- Ensure program runs to completion
- Check for early exits or errors
- Verify GPU is not throttling (check with `nvidia-smi`)

### Multiple GPUs
```bash
# Profile specific GPU
ncu --devices 0 ./program  # GPU 0
ncu --devices 1 ./program  # GPU 1
```

### Large Reports
For applications with many kernels or long execution times:
```bash
# Profile specific kernel only
ncu -k target_kernel ./program

# Use range profiling
ncu --range ncu_export.json ./program

# Limit report size
ncu --page-size 1000 ./program
```

## Optimization Strategies Based on NCU Output

### Memory Bound
If `dram__throughput.avg.pct_of_peak` is high:
- Check memory coalescing
- Use shared memory
- Optimize data layout
- Reduce global memory access

### Compute Bound
If compute metrics are high:
- Increase arithmetic intensity
- Use vectorized operations
- Optimize loop unrolling
- Check for instruction bottlenecks

### Low Occupancy
If `smsp__warps_active.avg.per_cycle_active` is low:
- Increase block size (up to 1024 threads)
- Reduce register usage
- Decrease shared memory per block
- Check resource limits

### Instruction Bottlenecks
Check `smsp__pipe_*_cycles_active` metrics:
- ALU bottleneck: Reduce arithmetic complexity
- FP bottleneck: Consider mixed precision
- Load/store bottleneck: Improve memory access patterns

## Integration with Development Workflow

### Automated Profiling Script
```bash
#!/bin/bash
# profile.sh - Automated NCU profiling

PROGRAM=$1
REPORT_DIR="ncu_reports"
mkdir -p $REPORT_DIR

# Clean build
make clean && make release

# Profile with full metrics
ncu --set full --mode=launch -o $REPORT_DIR/${PROGRAM}_$(date +%Y%m%d_%H%M%S) ./$PROGRAM
```

### CI/CD Integration
```yaml
# Example GitHub Actions workflow
- name: Profile CUDA Application
  run: |
    ncu --set normal --mode=launch -o benchmark_report ./benchmark
    ncu --export csv --import benchmark_report.ncu-rep > benchmark_results.csv
