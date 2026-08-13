---
name: ncu
description: NVIDIA Compute Utility (ncu) for CUDA kernel performance profiling and optimization. Use when analyzing CUDA application performance, identifying bottlenecks, or comparing different kernel implementations. Provides execution metrics, memory throughput, compute utilization, and occupancy data for performance tuning.
---

# NCU - CUDA Kernel Profiling

## Quick Start

Profile a CUDA program:

```bash
ncu --mode=launch ./your_cuda_program
```

For detailed analysis with report file:

```bash
ncu --set full --mode=launch -o my_report ./your_cuda_program
```

## Common Use Cases

### Profile entire application
```bash
ncu --mode=launch ./program
```

### Profile specific kernel
```bash
ncu -k kernel_name --mode=launch ./program
```

### Compare multiple implementations
```bash
ncu -o impl_v1 --mode=launch ./impl_v1
ncu -o impl_v2 --mode=launch ./impl_v2
ncu --import impl_v1.ncu-rep
ncu --import impl_v2.ncu-rep
```

## Key Performance Metrics

Focus on these metrics when analyzing results:

1. **Duration** - Kernel execution time (lower is better)
2. **Memory Throughput** - Data transfer rate GB/s (higher is better)
3. **Compute Throughput** - GFLOPS/TFLOPS (higher is better)
4. **Occupancy** - SM utilization 0-100% (generally higher is better)

## Report Analysis

View report summary:

```bash
ncu --import report.ncu-rep
```

Export to CSV:

```bash
ncu --export csv --import report.ncu-rep
```

## Performance Optimization

Based on NCU output, consider:

- **Memory optimization**: Use shared memory, coalesce global memory access
- **Occupancy improvement**: Adjust block/grid size, reduce register usage
- **Compute optimization**: Use appropriate precision, leverage CUDA intrinsics

## Advanced Usage

### Custom metrics
```bash
ncu --metrics dram__throughput.avg.pct_of_peak,smsp__sass_thread_inst_executed_op_fadd_pred_on.sum ./program
```

### Configuration sections
```bash
ncu --section SpeedOfLight ./program
```

## Best Practices

- Use release builds with optimizations enabled
- Avoid I/O operations during profiling
- Run multiple times and average results
- Be aware of profiling overhead (2-10x slowdown is normal)

## Reference Material

For detailed command-line options, API documentation, and optimization guides, see [references/ncu-guide.md](references/ncu-guide.md).
