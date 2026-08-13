#!/usr/bin/env bash
set -euo pipefail

invocation_args=("$@")
mode="${1:-screen}"
output_root="${2:-/tmp/dagger-egraph-bench}"
mkdir -p "$output_root/raw" "$output_root/profiles"

manifest="$output_root/manifest.tsv"
if [[ ! -f "$manifest" ]]; then
	printf 'phase\tfamily\tscale\treplicate\tstatus\toutcome\telapsed_s\tmax_rss_kib\tfile\n' >"$manifest"
fi

characterize_environment() {
	{
		printf 'phase=%s\n' "$mode"
		printf 'argv='
		printf '%q ' "${invocation_args[@]}"
		printf '\n'
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
			: <"$governor"
		done
		printf '\n'
	} >>"$output_root/environment-history.txt" 2>&1
}

sanitize() {
	printf '%s' "$1" | tr '/^$|()[]*+?. ' '_'
}

declare -A stopped

extract_max_rss() {
	local raw="$1"
	local max_rss
	max_rss="$(awk -F: '/Maximum resident set size \(kbytes\)/ {gsub(/^[[:space:]]+/, "", $2); print $2}' "$raw" | tail -n1)"
	printf '%s' "${max_rss:-0}"
}

classify_point_output() {
	local status="$1"
	local raw="$2"
	if [[ "$status" -eq 124 || "$status" -eq 143 ]]; then
		printf 'external-timeout'
	elif [[ "$status" -ne 0 ]]; then
		printf 'command-failure'
	else
		awk '
			/EGRAPH_BENCH_STOP/ { stopped = 1 }
			/^BenchmarkCacheEGraph.*-[0-9]+[[:space:]]+1[[:space:]]/ { result = 1 }
			END {
				# A setup or in-process memory guard skips the sub-benchmark
				# without a result line. Recognize that deliberate family stop
				# before validating ordinary successful output.
				if (stopped) print "benchmark-stop"
				else if (!result) print "missing-result"
				else print "completed"
			}
		' "$raw"
	fi
}

run_preflight() {
	local family="$1"
	local raw_name="$2"
	shift 2
	local raw="$output_root/raw/$raw_name"
	if [[ -e "$raw" ]]; then
		printf 'refusing to overwrite existing preflight output: %s\n' "$raw" >&2
		exit 2
	fi
	local started=$SECONDS
	set +e
	/usr/bin/time -v timeout --signal=TERM 75s "$@" >"$raw" 2>&1
	local status=$?
	set -e
	local elapsed=$((SECONDS - started))
	local max_rss
	max_rss="$(extract_max_rss "$raw")"
	local outcome=completed
	if [[ "$status" -eq 124 || "$status" -eq 143 ]]; then
		outcome=external-timeout
	elif [[ "$status" -ne 0 ]]; then
		outcome=command-failure
	elif (( max_rss > 4194304 )); then
		outcome=max-rss-failure
	fi
	printf 'preflight\t%s\t-\t1\t%s\t%s\t%s\t%s\t%s\n' \
		"$family" "$status" "$outcome" "$elapsed" "$max_rss" "$raw" >>"$manifest"
	if [[ "$status" -ne 0 ]]; then
		printf 'preflight failed or exceeded 75s: family=%s status=%s raw=%s\n' \
			"$family" "$status" "$raw" >&2
		exit "$status"
	fi
	if (( max_rss > 4194304 )); then
		printf 'preflight exceeded 4 GiB RSS: family=%s max_rss_kib=%s raw=%s\n' \
			"$family" "$max_rss" "$raw" >&2
		exit 1
	fi
}

run_point() {
	local phase="$1"
	local family="$2"
	local scale="$3"
	local regex="$4"
	local key="$phase/$family"
	if [[ -n "${stopped[$key]:-}" ]]; then
		return 0
	fi
	# The first pass is a scaling screen, not a precision estimate. Run each
	# point once; only a family selected by the observed shape is repeated in a
	# separate confirmation run.
	# Keep the singleton replicate axis loop-shaped for confirmation runs.
	# shellcheck disable=SC2043
	for replicate in 1; do
		local safe
		safe="$(sanitize "$phase-$family-$scale-r$replicate")"
		local raw="$output_root/raw/$safe.txt"
		if [[ -e "$raw" ]]; then
			printf 'refusing to overwrite existing benchmark output: %s\n' "$raw" >&2
			exit 2
		fi
		local started=$SECONDS
		set +e
		/usr/bin/time -v timeout --signal=TERM 75s \
			env DAGGER_EGRAPH_BENCH_SCALE="$scale" \
			go test ./dagql -run '^$' -bench "$regex" -benchtime=1x -count=1 -v \
			>"$raw" 2>&1
		local status=$?
		set -e
		local outcome
		outcome="$(classify_point_output "$status" "$raw")"
		if [[ "$outcome" == missing-result ]]; then
			printf 'runner validation: benchmark emitted no one-iteration result line\n' >>"$raw"
		fi
		local elapsed=$((SECONDS - started))
		local max_rss
		max_rss="$(extract_max_rss "$raw")"
		if [[ "$outcome" == completed ]] && (( max_rss > 4194304 )); then
			outcome=max-rss-stop
		fi
		printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
			"$phase" "$family" "$scale" "$replicate" "$status" "$outcome" "$elapsed" "$max_rss" "$raw" >>"$manifest"

		case "$outcome" in
		external-timeout)
			stopped[$key]=1
			printf 'stopping larger points: family=%s scale=%s outcome=%s status=%s raw=%s\n' \
				"$family" "$scale" "$outcome" "$status" "$raw"
			return 0
			;;
		benchmark-stop)
			stopped[$key]=1
			printf 'stopping larger points: family=%s scale=%s outcome=%s raw=%s\n' \
				"$family" "$scale" "$outcome" "$raw"
			return 0
			;;
		max-rss-stop)
			stopped[$key]=1
			printf 'stopping larger points: family=%s scale=%s outcome=%s max_rss_kib=%s raw=%s\n' \
				"$family" "$scale" "$outcome" "$max_rss" "$raw"
			return 0
			;;
		command-failure)
			printf 'benchmark failed: family=%s scale=%s replicate=%s outcome=%s status=%s raw=%s\n' \
				"$family" "$scale" "$replicate" "$outcome" "$status" "$raw" >&2
			exit "$status"
			;;
		missing-result)
			printf 'benchmark failed: family=%s scale=%s replicate=%s outcome=%s status=%s raw=%s\n' \
				"$family" "$scale" "$replicate" "$outcome" "$status" "$raw" >&2
			exit 1
			;;
		completed) ;;
		*) printf 'internal runner error: unknown outcome=%s\n' "$outcome" >&2; exit 1 ;;
		esac
	done
}

run_unscaled_point() {
	local phase="$1"
	local family="$2"
	local regex="$3"
	run_point "$phase" "$family" 64 "$regex"
}

run_correctness() {
	run_preflight correctness correctness.txt \
		go test ./dagql -run 'TestCacheEGraphBenchmark|TestEGraphBenchmarkDistributions' -count=1
}

run_screen() {
	run_correctness

	for persistence in transient imported; do
		for scale in 10000 50000 200000; do
			run_point serial "release-independent-$persistence" "$scale" \
				"^BenchmarkCacheEGraphRelease/independent/$persistence/$scale$"
		done
	done
	for shape in chain star-fanout star-shared; do
		for scale in 1000 10000 50000; do
			run_point serial "release-$shape" "$scale" \
				"^BenchmarkCacheEGraphRelease/$shape/transient/$scale$"
		done
	done
	for persistence in transient imported; do
		for scale in 256 512 1024 2048; do
			run_point serial "release-wide-output-$persistence" "$scale" \
				"^BenchmarkCacheEGraphRelease/wide-output/$persistence/$scale$"
		done
	done
	for scale in 1000 10000; do
		run_point serial release-wide-digest "$scale" \
			"^BenchmarkCacheEGraphRelease/wide-digest/transient/$scale$"
	done

	for route in exact-recipe shared-extra structural; do
		for persistence in transient imported; do
			# Keep the singleton ownership axis loop-shaped for future modes.
			# shellcheck disable=SC2043
			for ownership in fresh-session; do
				for scale in 256 512 1024 2048; do
					run_point serial "lookup-$route-$persistence-$ownership" "$scale" \
						"^BenchmarkCacheEGraphLookup/$route/$persistence/$ownership/$scale$"
				done
			done
		done
	done

	for operation in direct receiver; do
		for persistence in transient imported; do
			# Keep the singleton ownership axis loop-shaped for future modes.
			# shellcheck disable=SC2043
			for ownership in fresh-session; do
				for scale in 256 512 1024 2048; do
					run_point serial "id-$operation-$persistence-$ownership" "$scale" \
						"^BenchmarkCacheEGraphIDLoad/$operation/$persistence/$ownership/$scale$"
				done
			done
		done
	done

	for publication in distinct-class join-wide-class; do
		for scale in 256 512 1024 2048; do
			run_point serial "publication-$publication" "$scale" \
				"^BenchmarkCacheEGraphPublication/publish/$publication/$scale$"
		done
	done
	for persistence in transient imported; do
		for terms in 1000 10000 50000; do
			for merges in 1 64; do
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
				"^BenchmarkCacheEGraphPrune/$shape/in-memory-persisted-roots/$scale$"
		done
	done
	for persistence in persisted-fresh imported; do
		for scale in 256 512 1024 2048; do
			run_point serial "prune-wide-output-$persistence" "$scale" \
				"^BenchmarkCacheEGraphPrune/wide-output/$persistence/$scale$"
		done
	done
	for scale in 1000 10000; do
		run_point serial policy-prune-representative "$scale" \
			"^BenchmarkCacheEGraphPolicyPruneRepresentative/$scale$"
	done

	local max_workers=24
	if (( max_workers > $(nproc) )); then
		max_workers="$(nproc)"
	fi
	local worker_counts=(1)
	if (( max_workers != 1 )); then
		worker_counts+=("$max_workers")
	fi
	for operation in exact-recipe id-load; do
		for persistence in transient imported; do
			for workers in "${worker_counts[@]}"; do
				run_unscaled_point steady "steady-$operation-$persistence-workers-$workers" \
					"^BenchmarkCacheEGraphSteadyState/$operation/$persistence/workers-$workers$"
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
	local claim="$output_root/profiles/.first-anomaly-in-progress"
	if ! mkdir "$claim" 2>/dev/null; then
		printf 'a first-anomaly profile is already in progress at %s\n' "$claim" >&2
		exit 2
	fi
	local attempt_id
	attempt_id="$(date -u +'%Y%m%dT%H%M%SZ')-$$"
	local attempt="$output_root/profiles/attempt-$attempt_id"
	mkdir "$attempt"
	local raw="$output_root/raw/profile-first-anomaly-$attempt_id.txt"
	printf 'scale=%s\nregex=%s\nraw=%s\n' "$scale" "$regex" "$raw" >"$attempt/request.txt"
	local started=$SECONDS
	set +e
	/usr/bin/time -v timeout --signal=TERM 75s env DAGGER_EGRAPH_BENCH_SCALE="$scale" go test ./dagql -run '^$' -bench "$regex" \
		-benchtime=1x -count=1 -v \
		-cpuprofile "$attempt/cpu.pprof" \
		-memprofile "$attempt/heap.pprof" \
		-mutexprofile "$attempt/mutex.pprof" \
		-blockprofile "$attempt/block.pprof" \
		>"$raw" 2>&1
	local status=$?
	set -e
	local outcome
	outcome="$(classify_point_output "$status" "$raw")"
	if [[ "$outcome" == missing-result ]]; then
		printf 'runner validation: profile emitted no one-iteration result line\n' >>"$raw"
	fi
	local elapsed=$((SECONDS - started))
	local max_rss
	max_rss="$(extract_max_rss "$raw")"
	if [[ "$outcome" == completed ]] && (( max_rss > 4194304 )); then
		outcome=max-rss-failure
	fi
	printf 'profile\tfirst-anomaly\t%s\t1\t%s\t%s\t%s\t%s\t%s\n' \
		"$scale" "$status" "$outcome" "$elapsed" "$max_rss" "$raw" >>"$manifest"
	if [[ "$outcome" != completed ]]; then
		printf 'status=%s\noutcome=%s\n' "$status" "$outcome" >"$attempt/FAILED"
		rmdir "$claim"
		printf 'profile failed: outcome=%s status=%s raw=%s\n' "$outcome" "$status" "$raw" >&2
		if [[ "$status" -ne 0 ]]; then
			exit "$status"
		fi
		exit 1
	fi
	printf 'scale=%s\nregex=%s\nraw=%s\nattempt=%s\n' \
		"$scale" "$regex" "$raw" "$attempt" >"$attempt/SUCCESS"
	printf 'scale=%s\nregex=%s\nraw=%s\nattempt=%s\n' \
		"$scale" "$regex" "$raw" "$attempt" >"$marker.tmp"
	mv "$marker.tmp" "$marker"
	rmdir "$claim"
}

characterize_environment
case "$mode" in
	screen) run_screen ;;
	contention) run_contention "${3:-}" ;;
	profile) run_profile "${3:-}" "${4:-}" ;;
	*) printf 'usage: %s {screen|contention|profile} OUTPUT_DIR [contention-kind|scale] [benchmark-regex]\n' "$0" >&2; exit 2 ;;
esac
