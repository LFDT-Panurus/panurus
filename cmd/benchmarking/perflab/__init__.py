"""PerfLab: continuous performance-regression infrastructure for Panurus.

See ``docs/development/perflab.md`` for the operator runbook and
``docs/drivers/benchmark/core/dlognogh/dlognogh.md`` for the benchmarks being
measured. This package is deployed by cloning the repository onto the
benchmark host and running ``python -m perflab.cli ...`` from
``cmd/benchmarking/`` (it imports ``bench_parse`` and ``compare_benchmarks``
as sibling modules, so it must not be moved out of ``cmd/benchmarking/``).
"""
