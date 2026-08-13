#!/usr/bin/env bash
set -euo pipefail

mode="${1:-screen}"
output_root="${2:-/tmp/dagger-egraph-bench}"
mkdir -p "$output_root/raw" "$output_root/profiles"

manifest="$output_root/manifest.tsv"
if [[ ! -f "$manifest" ]]; then
	printf 'phase\tfamily\tscale\treplicate\tstatus\telapsed_s\tmax_rss_kib\tfile\n' >"$manifest"
fi

characterize_environment() {
	{
		date -u +'%Y-%m-%dT%H:%M:%SZ'
		uname -a
		go version
		go env GOOS GOARCH GOVERSION GOMAXPROCS GOAMD64
		git rev-parse HEAD
		git status --short
		nproc
		free -b || true
		lscpu || true
		ulimit -a || true
		for governor in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do
			[[ -r "$governor" ]] || continue
			printf '%s=' "$governor"
			<"$governor"
		done
	} >"$output_root/environment.txt" 2>&1
}

sanitize() {
	printf '%s' "$1" | tr '/^$|()[]*+?. ' '_'
}

declare -A stopped

run_point() {
	local phase="$1"
	local family="$2"
	local scale="$3"
	local regex="$4"
	local key="$phase/$family"
	if [[ -n "${stopped[$key]:-}" ]]; then
		return 0
	fi
	for replicate in 1 2 3; do
		local safe
		safe="$(sanitize "$phase-$family-$scale-r$replicate")"
		local raw="$output_root/raw/$safe.txt"
		local started=$SECONDS
		set +e
		/usr/bin/time -v timeout --signal=TERM 75s \
			env DAGGER_EGRAPH_BENCH_SCALE="$scale" \
			go test ./dagql -run '^$' -bench "$regex" -benchtime=1x -count=1 -v \
			>"$raw" 2>&1
		local status=$?
		set -e
		local elapsed=$((SECONDS - started))
		local max_rss
		max_rss="$(awk -F: '/Maximum resident set size \(kbytes\)/ {gsub(/^[[:space:]]+/, "", $2); print $2}' "$raw" | tail -n1)"
		max_rss="${max_rss:-0}"
		printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
			"$phase" "$family" "$scale" "$replicate" "$status" "$elapsed" "$max_rss" "$raw" >>"$manifest"

		if [[ "$status" -ne 0 ]]; then
			if [[ "$status" -eq 124 || "$status" -eq 143 ]]; then
				stopped[$key]=1
				printf 'stopping larger points: family=%s scale=%s reason=external-timeout status=%s raw=%s\n' \
					"$family" "$scale" "$status" "$raw"
				return 0
			fi
			printf 'benchmark failed: family=%s scale=%s replicate=%s status=%s raw=%s\n' \
				"$family" "$scale" "$replicate" "$status" "$raw" >&2
			exit "$status"
		fi
		if grep -q 'EGRAPH_BENCH_STOP' "$raw"; then
			stopped[$key]=1
			printf 'stopping larger points: family=%s scale=%s reason=benchmark-limit raw=%s\n' \
				"$family" "$scale" "$raw"
			return 0
		fi
		if (( max_rss > 4194304 )); then
			stopped[$key]=1
			printf 'stopping larger points: family=%s scale=%s reason=max-rss max_rss_kib=%s raw=%s\n' \
				"$family" "$scale" "$max_rss" "$raw"
			return 0
		fi
		if ! grep -Eq '^BenchmarkCacheEGraph.*-[0-9]+[[:space:]]+1[[:space:]]' "$raw"; then
			printf 'benchmark emitted no result line: family=%s scale=%s raw=%s\n' "$family" "$scale" "$raw" >&2
			exit 1
		fi
	done
}

run_unscaled_point() {
	local phase="$1"
	local family="$2"
	local regex="$3"
	run_point "$phase" "$family" 64 "$regex"
}

run_correctness_and_overhead() {
	go test ./dagql -run 'TestCacheEGraphBenchmark|TestEGraphBenchmarkDistributions' -count=1 \
		>"$output_root/raw/correctness.txt" 2>&1
	go test ./dagql -run '^$' -bench '^BenchmarkCacheEGraphInstrumentationOverhead$' \
		-benchtime=100000x -count=1 -v >"$output_root/raw/instrumentation-overhead.txt" 2>&1
	local overhead_raw="$output_root/raw/instrumentation-overhead.txt"
	local disabled_median
	local enabled_median
	local paired_overhead_median
	disabled_median="$(awk 'index($1, "/disabled-") && $4 == "ns/op" {print $3}' "$overhead_raw" | sort -n | awk '{v[NR]=$1} END {if (NR > 0) print v[int((NR+1)/2)]}')"
	enabled_median="$(awk 'index($1, "/enabled-") && $4 == "ns/op" {print $3}' "$overhead_raw" | sort -n | awk '{v[NR]=$1} END {if (NR > 0) print v[int((NR+1)/2)]}')"
	paired_overhead_median="$(awk '
		index($1, "/disabled-") && $4 == "ns/op" {key=$1; sub(/\/disabled-.*/, "", key); disabled[key]=$3}
		index($1, "/enabled-") && $4 == "ns/op" {key=$1; sub(/\/enabled-.*/, "", key); enabled[key]=$3}
		END {for (key in disabled) if (key in enabled) print 100 * (enabled[key]-disabled[key]) / disabled[key]}
	' "$overhead_raw" | sort -n | awk '{v[NR]=$1} END {if (NR > 0) printf "%.3f", v[int((NR+1)/2)]}')"
	if [[ -z "$disabled_median" || -z "$enabled_median" || -z "$paired_overhead_median" ]]; then
		printf 'could not parse instrumentation overhead medians from %s\n' "$overhead_raw" >&2
		exit 1
	fi
	printf 'disabled_median_ns_per_op=%s\nenabled_median_ns_per_op=%s\npaired_overhead_median_percent=%s\n' \
		"$disabled_median" "$enabled_median" "$paired_overhead_median" >"$output_root/instrumentation-gate.txt"
	if awk -v overhead="$paired_overhead_median" 'BEGIN {exit !(overhead > 5)}'; then
		printf 'instrumentation overhead %.3f%% exceeds 5%%; stop for review (%s)\n' \
			"$paired_overhead_median" "$overhead_raw" >&2
		exit 1
	fi
}

run_screen() {
	run_correctness_and_overhead

	for persistence in fresh imported; do
		for scale in 10000 50000 200000; do
			run_point serial "release-independent-$persistence" "$scale" \
				"^BenchmarkCacheEGraphRelease/independent/$persistence/$scale$"
		done
	done
	for shape in chain star-fanout star-shared; do
		for scale in 1000 10000 50000; do
			run_point serial "release-$shape" "$scale" \
				"^BenchmarkCacheEGraphRelease/$shape/fresh/$scale$"
		done
	done
	for persistence in fresh imported; do
		for scale in 64 128 256 512 1024 2048; do
			run_point serial "release-wide-output-$persistence" "$scale" \
				"^BenchmarkCacheEGraphRelease/wide-output/$persistence/$scale$"
		done
	done
	for scale in 1000 10000; do
		run_point serial release-wide-digest "$scale" \
			"^BenchmarkCacheEGraphRelease/wide-digest/fresh/$scale$"
	done

	for route in exact-recipe shared-extra structural; do
		for persistence in fresh imported; do
			for ownership in fresh-session same-session-repeat; do
				for scale in 64 128 256 512 1024 2048; do
					run_point serial "lookup-$route-$persistence-$ownership" "$scale" \
						"^BenchmarkCacheEGraphLookup/$route/$persistence/$ownership/$scale$"
				done
			done
		done
	done

	for operation in direct receiver; do
		for persistence in fresh imported; do
			for ownership in fresh-session same-session-repeat; do
				for scale in 64 128 256 512 1024 2048; do
					run_point serial "id-$operation-$persistence-$ownership" "$scale" \
						"^BenchmarkCacheEGraphIDLoad/$operation/$persistence/$ownership/$scale$"
				done
			done
		done
	done

	for publication in distinct-class join-wide-class; do
		for scale in 64 128 256 512 1024 2048; do
			run_point serial "publication-$publication" "$scale" \
				"^BenchmarkCacheEGraphPublication/publish/$publication/$scale$"
		done
	done
	for persistence in fresh imported; do
		for terms in 1000 10000 50000; do
			for merges in 1 8 64; do
				run_point serial "popular-$persistence-merges-$merges" "$terms" \
					"^BenchmarkCacheEGraphPublication/popular-input/$persistence/terms-$terms/merges-$merges$"
			done
		done
	done

	for persistence in persisted-fresh imported; do
		for scale in 10000 50000 200000; do
			run_point serial "prune-independent-$persistence" "$scale" \
				"^BenchmarkCacheEGraphPrune/independent/$persistence/$scale$"
		done
	done
	for shape in chain star-fanout star-shared; do
		for scale in 1000 10000 50000; do
			run_point serial "prune-$shape" "$scale" \
				"^BenchmarkCacheEGraphPrune/$shape/persisted-fresh/$scale$"
		done
	done
	for persistence in persisted-fresh imported; do
		for scale in 64 128 256 512 1024 2048; do
			run_point serial "prune-wide-output-$persistence" "$scale" \
				"^BenchmarkCacheEGraphPrune/wide-output/$persistence/$scale$"
		done
	done
	for scale in 1000 10000; do
		run_point serial disk-prune-representative "$scale" \
			"^BenchmarkCacheEGraphDiskPruneRepresentative/$scale$"
	done

	for operation in exact-recipe id-load; do
		for persistence in fresh imported; do
			for workers in 1 24; do
				local actual_workers="$workers"
				if (( workers > $(nproc) )); then
					actual_workers="$(nproc)"
				fi
				run_unscaled_point steady "steady-$operation-$persistence-workers-$actual_workers" \
					"^BenchmarkCacheEGraphSteadyState/$operation/$persistence/workers-$actual_workers$"
			done
		done
	done
}

run_contention() {
	local selected="${1:-}"
	case "$selected" in
		release|prune|teach) ;;
		*) printf 'contention mode requires release, prune, or teach\n' >&2; exit 2 ;;
	esac
	local workers=24
	if (( workers > $(nproc) )); then
		workers="$(nproc)"
	fi
	run_unscaled_point contention "contention-$selected-workers-1" \
		"^BenchmarkCacheEGraphContention/$selected/workers-1$"
	if [[ "$workers" != 1 ]]; then
		run_unscaled_point contention "contention-$selected-workers-$workers" \
			"^BenchmarkCacheEGraphContention/$selected/workers-$workers$"
	fi
}

run_profile() {
	local scale="${1:-}"
	local regex="${2:-}"
	if [[ -z "$scale" || -z "$regex" ]]; then
		printf 'profile mode requires SCALE and exact BENCHMARK_REGEX\n' >&2
		exit 2
	fi
	local marker="$output_root/profiles/FIRST_ANOMALY_ONLY"
	if [[ -e "$marker" ]]; then
		printf 'a first-anomaly profile already exists at %s\n' "$marker" >&2
		exit 2
	fi
	local raw="$output_root/raw/profile-first-anomaly.txt"
	printf 'scale=%s\nregex=%s\nraw=%s\n' "$scale" "$regex" "$raw" >"$marker"
	set +e
	/usr/bin/time -v timeout --signal=TERM 75s env DAGGER_EGRAPH_BENCH_SCALE="$scale" go test ./dagql -run '^$' -bench "$regex" \
		-benchtime=1x -count=1 -v \
		-cpuprofile "$output_root/profiles/cpu.pprof" \
		-memprofile "$output_root/profiles/heap.pprof" \
		-mutexprofile "$output_root/profiles/mutex.pprof" \
		-blockprofile "$output_root/profiles/block.pprof" \
		>"$raw" 2>&1
	local status=$?
	set -e
	if [[ "$status" -ne 0 ]]; then
		printf 'profile failed or exceeded 75s: status=%s raw=%s\n' "$status" "$raw" >&2
		exit "$status"
	fi
	local max_rss
	max_rss="$(awk -F: '/Maximum resident set size \(kbytes\)/ {gsub(/^[[:space:]]+/, "", $2); print $2}' "$raw" | tail -n1)"
	max_rss="${max_rss:-0}"
	if (( max_rss > 4194304 )); then
		printf 'profile exceeded 4 GiB RSS: max_rss_kib=%s raw=%s\n' "$max_rss" "$raw" >&2
		exit 1
	fi
}

characterize_environment
case "$mode" in
	screen) run_screen ;;
	contention) run_contention "${3:-}" ;;
	profile) run_profile "${3:-}" "${4:-}" ;;
	*) printf 'usage: %s {screen|contention|profile} OUTPUT_DIR [contention-kind|scale] [benchmark-regex]\n' "$0" >&2; exit 2 ;;
esac
