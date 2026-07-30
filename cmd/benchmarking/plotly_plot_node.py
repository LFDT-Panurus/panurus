import re
import os
from pathlib import Path
from typing import Any
import pandas as pd
import plotly.express as px
import plotly.io as pio
import streamlit as st

# Parsing logic lives in a dependency-free module so it can be reused by the
# PR benchmark-comparison tool without importing plotly/streamlit.
from bench_parse import simple_parser

st.set_page_config(
    layout="wide", page_icon=":chart_with_upwards_trend:",
    page_title="TPS Degradation", initial_sidebar_state="collapsed")

IGNORE_COLS = {"bench", "workers", "tps",
               "iterations", "ns/op", "B/op", "allocs/op"}
DEFAULT_BENCH_DIR = "bench"
SHAPES = ['circle', 'square', 'diamond', 'cross', 'hexagon', 'star']


# --- STRUCTURED PARALLEL LOG PARSER ---

ANSI_ESCAPE_RE = re.compile(r'\x1b\[[0-9;]*m')
PARALLEL_RUN_RE = re.compile(r'=== RUN\s+(\S+)/(.+?)_with_(\d+)_workers')
PARALLEL_THROUGHPUT_RE = re.compile(r'Pure Throughput\s+([\d.]+)/s')
PARALLEL_LATENCY_RE = re.compile(
    r'(Min|P50 \(Median\)|Average|P95|P99\.9|P99|P5|Max)\s+'
    r'([\d.]+(?:ms|[n\xb5\xc2]+s|s))')
WORKERS_RE = re.compile(r'^Workers\s+(\d+)')

# Latency keys we keep, mapped from log label -> column name.
LATENCY_KEYS = {
    'P50 (Median)': 'p50 (ms)',
    'P5': 'p5 (ms)',
    'P95': 'p95 (ms)',
    'P99': 'p99 (ms)',
}
DISPLAY_LATENCY = {'avg (ms)', 'p50 (ms)', 'p95 (ms)'}

# Multipliers to convert a latency unit into milliseconds.
_MS_PER_UNIT = {'ms': 1, 'ns': 1e-6, 's': 1e3}


def _parse_ms(s: str) -> float:
    m = re.match(r'([\d.]+)(.*)', s)
    val, unit = float(m.group(1)), m.group(2)  # type: ignore
    if '\xb5' in unit or 'µ' in unit:
        return val / 1e3
    if unit in _MS_PER_UNIT:
        return val * _MS_PER_UNIT[unit]
    raise ValueError(f"unknown unit: {unit}")


def _parse_setup(params_raw: str) -> dict:
    """Extract ``k=v`` pairs from a ``Setup(#key_val,_...)`` fragment."""
    setup = {}
    m = re.search(r'Setup\((.+?)\)', params_raw)
    for token in (m.group(1).split(',_') if m else []):
        token = token.strip('_').lstrip('#')
        if '_' in token:
            k, v = token.split('_', 1)
            setup[k] = v
    return setup


def parse_parallel_log(path: Path, default_bench: str | None = None) -> pd.DataFrame:
    text = ANSI_ESCAPE_RE.sub('', path.read_text())
    has_run_lines = bool(PARALLEL_RUN_RE.search(text))
    rows: list[dict] = []
    current: dict = {}

    for line in (ln.strip() for ln in text.splitlines()):
        if m := PARALLEL_RUN_RE.match(line):
            if current:
                rows.append(current)
            current = {'bench': m.group(1), 'workers': int(m.group(3)),
                       **_parse_setup(m.group(2))}
        elif not has_run_lines and (m := WORKERS_RE.match(line)):
            if current:
                rows.append(current)
            current = {'bench': default_bench or path.stem,
                       'workers': int(m.group(1))}
        elif m := PARALLEL_THROUGHPUT_RE.match(line):
            current['tps'] = float(m.group(1))
        elif (m := PARALLEL_LATENCY_RE.match(line)) and m.group(1) in LATENCY_KEYS:
            current[LATENCY_KEYS[m.group(1)]] = _parse_ms(m.group(2))
        elif m := re.match(r'Total Ops\s+(\d+)', line):
            current['iterations'] = int(m.group(1))

    if current:
        rows.append(current)
    return pd.DataFrame(rows)


def has_multi_nc(df: pd.DataFrame) -> bool:
    return "nc" in df.columns and df["nc"].nunique() > 1


# --- PLOTTING ---


def _style(fig):
    fig.update_layout(template='plotly_white', hovermode='x unified')
    return fig


def _tps_fig(df, color_col, title):
    fig = px.line(df, x='workers', y='tps', color=color_col, symbol=color_col,
                  markers=True, title=title, symbol_sequence=SHAPES,
                  labels={'workers': 'Workers', 'tps': 'TPS', color_col: ''})
    fig.update_yaxes(nticks=25)
    return _style(fig)


def _latency_fig(df, color_col, dash_col, title):
    latency_cols = [c for c in df.columns if c in DISPLAY_LATENCY]
    if not latency_cols:
        return None
    melted = df.melt(id_vars=[color_col, 'workers'], value_vars=latency_cols,
                     var_name='percentile', value_name='latency')
    dash_map = {'avg (ms)': 'dash', 'p50 (ms)': 'solid', 'p95 (ms)': 'dot'}
    return _style(px.line(
        melted, x='workers', y='latency', color=color_col, line_dash=dash_col,
        markers=True, title=title, line_dash_map=dash_map,
        labels={'workers': 'Workers', 'latency': 'Latency (ms)', color_col: ''}))


def _aggregate(df, group_cols):
    numeric = [c for c in df.select_dtypes(include='number').columns
               if c != 'workers']
    return df.groupby(group_cols)[numeric].mean().reset_index().sort_values('workers')


def make_figures(df):
    param_cols = [c for c in df.columns
                  if c not in IGNORE_COLS and not c.endswith("(ms)")]
    figs = []

    for bench, bdf in df.groupby('bench'):
        bdf = bdf.copy()
        varying = [c for c in param_cols if c in bdf and bdf[c].nunique() > 1]
        fixed = [c for c in param_cols
                 if c in bdf and bdf[c].nunique() <= 1 and bdf[c].notna().any()]

        bdf['series'] = (
            bdf[varying].astype(str).apply(
                lambda r: ', '.join(f'{k}={v}' for k, v in r.items()), axis=1)
            if varying else bench
        )

        agg = _aggregate(bdf, ['series', 'workers'])
        fixed_str = ', '.join(str(bdf[c].dropna().iloc[0]) for c in fixed)
        suffix = f' ({fixed_str})' if fixed_str else ''

        figs.append(_tps_fig(agg, 'series', f'{bench}{suffix}'))
        lat = _latency_fig(agg, 'series', 'percentile',
                           f'{bench} - Latency{suffix}')
        if lat:
            figs.append(lat)

    return figs


def parse_combined(dfs: dict[str, pd.DataFrame]):
    for name, df in dfs.items():
        df['bench'] = name
    dct = {}
    for df in dfs.values():
        for key, group in df.groupby("nc"):
            dct[key] = pd.concat(
                [dct.get(key, pd.DataFrame()), group], ignore_index=True)
    return dct


def _with_bench(dfs: dict[str, pd.DataFrame]) -> list[pd.DataFrame]:
    """Copy each frame and set its ``bench`` column to the dict key."""
    parts = []
    for name, df in dfs.items():
        df = df.copy()
        df["bench"] = name
        parts.append(df)
    return parts


def make_combined_figures(dfs, local_dfs):
    figs: dict = {"_All": None}
    all_dfs = [_aggregate(d, ["bench", "workers"])
               for d in _with_bench(local_dfs)]
    local_parts = _with_bench(local_dfs)

    for nc, df in sorted(dfs.items()):
        agg = _aggregate(df, ["bench", "workers"])
        d = agg.copy()
        d["bench"] = d["bench"] + f" (nc={nc})"
        all_dfs.append(d)

        agg = _aggregate(
            pd.concat([agg, *local_parts], ignore_index=True),
            ["bench", "workers"])
        figs[nc] = _tps_fig(agg, 'bench', f'TPS (nc={nc})')
        lat = _latency_fig(agg, 'bench', 'percentile', f'Latency (nc={nc})')
        if lat:
            figs[f"{nc}_latency"] = lat

    figs["_All"] = _tps_fig(
        pd.concat(all_dfs, ignore_index=True), 'bench', 'TPS (All)')
    return figs


def _get_dir():
    def_dir = Path(os.environ.get("DEF_BENCH", DEFAULT_BENCH_DIR))

    with st.sidebar:
        directory = st.text_input(
            "Directory", value=str(def_dir) if def_dir.exists() else "",
            key="benchdir")
        os.environ["DEF_BENCH"] = directory

    if not directory:
        st.info("Please input a Folder in the Sidebar")
        return None
    directory = Path(directory)
    if not directory.exists():
        st.error(f"Directory `{directory}` does not exist")
        return None
    return directory


def _show(name: str, df: pd.DataFrame):
    with st.expander(f"`{name}`"):
        for fig in make_figures(df):
            st.plotly_chart(fig, key=f"{name}-{fig.layout.title.text}")
        st.dataframe(df)


def _load(directory: Path, pattern: str, parse) -> dict[str, pd.DataFrame]:
    out = {}
    for path in sorted(directory.glob(pattern)):
        df = parse(path)
        if not df.empty:
            _show(path.stem, df)
            out[path.stem] = df
    return out


def main():
    directory = _get_dir()
    if not directory:
        return

    single = _load(directory, "*.log", parse_parallel_log)
    all_dfs = _load(directory, "*.txt", simple_parser)

    multi = {n: df.copy() for n, df in all_dfs.items() if has_multi_nc(df)}
    single.update({n: df.copy() for n, df in all_dfs.items()
                   if not has_multi_nc(df)})

    figs = make_combined_figures(parse_combined(multi), local_dfs=single)
    st.subheader("Combined TPS")
    for fig, tab in zip(figs.values(), st.tabs(list(figs.keys()))):
        with tab:
            st.plotly_chart(fig)


if __name__ == "__main__":
    from argparse import ArgumentParser

    parser = ArgumentParser()
    parser.add_argument(
        "--single", action="store_true", help="Only plot single benchmark")
    parser.add_argument("--input", type=str, default=None)
    parser.add_argument("--output", type=str, default="out.html")

    args = parser.parse_args()
    if not args.single:
        main()
    else:
        assert args.input, "Missing --input argument"
        df = simple_parser(Path(args.input))
        figs = make_figures(df=df)
        print("Writing to", args.output)
        if args.output.endswith((".svg", ".png", ".jpeg")):
            pio.write_images(figs, args.output)
        elif args.output.endswith(".html"):
            for fig in figs:
                pio.write_html(fig, args.output)
        elif args.output.endswith(".json"):
            df.to_json(args.output, indent=2)
        else:
            raise ValueError(f"Unsupported output extension: {args.output}")
