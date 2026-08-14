#!/usr/bin/env bash

set -euo pipefail

readonly DEFAULT_THRESHOLD="80"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_bin="${GO:-go}"
tool_dir="${repo_root}/.tools/bin"
filter_bin="${tool_dir}/filter-go-coverage"
cache_dir="${repo_root}/.gocache"
gopath_dir="${repo_root}/.gopath"
report_dir="${repo_root}/.coverage/go-statement"
threshold="${GO_COVERAGE_THRESHOLD:-${DEFAULT_THRESHOLD}}"

export GOCACHE="${GOCACHE:-${cache_dir}}"
export GOPATH="${GOPATH:-${gopath_dir}}"
export GOMODCACHE="${GOMODCACHE:-${gopath_dir}/pkg/mod}"

is_generated_file() {
  local file="$1"
  if command -v rg >/dev/null 2>&1; then
    rg -q '^// Code generated .* DO NOT EDIT\.$' "${file}"
  else
    grep -Eq '^// Code generated .* DO NOT EDIT\.$' "${file}"
  fi
}

meets_threshold() {
  local percent="$1"
  local required="$2"
  awk -v percent="${percent}" -v required="${required}" 'BEGIN { exit !(percent + 0.0000001 >= required) }'
}

list_cover_packages() {
  local pattern="$1"
  # 包模式本身不允许包含空白，因此这里有意让 shell 展开多个模式。
  # shellcheck disable=SC2086
  "${go_bin}" list ${pattern}
}

write_generated_files() {
  local pattern="$1"
  local output="$2"
  local metadata
  metadata="$(mktemp "${TMPDIR:-/tmp}/bpt-go-cover-packages.XXXXXX")"
  # 包模式本身不允许包含空白，因此这里有意让 shell 展开多个模式。
  # shellcheck disable=SC2086
  "${go_bin}" list -f '{{.ImportPath}}|{{.Dir}}|{{join .GoFiles ","}}' ${pattern} > "${metadata}"
  : > "${output}"
  while IFS='|' read -r import_path package_dir files_csv; do
    [[ -n "${import_path}" ]] || continue
    local files=()
    local file
    IFS=',' read -r -a files <<< "${files_csv}"
    for file in "${files[@]}"; do
      [[ -n "${file}" ]] || continue
      if is_generated_file "${package_dir}/${file}"; then
        printf '%s/%s\n' "${import_path}" "${file}" >> "${output}"
      fi
    done
  done < "${metadata}"
  rm -f "${metadata}"
}

run_group() {
  local name="$1"
  local cover_pattern="$2"
  shift 2
  local test_targets=("$@")
  local packages_file="${report_dir}/${name}.packages.txt"
  local generated_file="${report_dir}/${name}.generated.txt"
  local raw_profile="${report_dir}/${name}.raw.out"
  local filtered_profile="${repo_root}/coverage-${name}.out"
  local function_report="${repo_root}/coverage-${name}.txt"
  local cover_packages

  list_cover_packages "${cover_pattern}" > "${packages_file}"
  cover_packages="$(paste -sd, "${packages_file}")"
  if [[ -z "${cover_packages}" ]]; then
    printf '%s 没有可覆盖的 Go 包\n' "${name}" >&2
    return 1
  fi
  write_generated_files "${cover_pattern}" "${generated_file}"

  printf '\n[%s] 运行完整包语句覆盖率\n' "${name}"
  # coverpkg 会为每个测试二进制写入全量源码块；禁用缓存以免混入改动前的行区间。
  "${go_bin}" test -count=1 "${test_targets[@]}" -coverpkg="${cover_packages}" -coverprofile="${raw_profile}"
  "${filter_bin}" "${raw_profile}" "${filtered_profile}" "${generated_file}"
  "${go_bin}" tool cover -func="${filtered_profile}" | tee "${function_report}"
}

coverage_percent() {
  local report="$1"
  awk '
    /^total:/ {
      value = $NF
      sub(/%$/, "", value)
      print value
      found = 1
    }
    END { if (!found) exit 1 }
  ' "${report}"
}

main() {
  mkdir -p "${tool_dir}" "${report_dir}" "${GOCACHE}" "${GOMODCACHE}"
  "${go_bin}" build -o "${filter_bin}" "${repo_root}/scripts/filter_go_coverage"

  run_group manager './manager/...' ./manager/...
  run_group worker './worker/...' ./worker/...
  run_group pkg './pkg/...' ./pkg/...
  cat "${repo_root}/coverage-manager.txt" \
    "${repo_root}/coverage-worker.txt" \
    "${repo_root}/coverage-pkg.txt" > "${repo_root}/coverage.txt"

  local failed=0
  local name
  for name in manager worker pkg; do
    local percent
    percent="$(coverage_percent "${repo_root}/coverage-${name}.txt")"
    printf '%s 手写生产 Go 代码语句覆盖率：%s%%，阈值 %s%%\n' "${name}" "${percent}" "${threshold}"
    if ! meets_threshold "${percent}" "${threshold}"; then
      printf '%s 语句覆盖率 %s%% 低于 %s%%\n' "${name}" "${percent}" "${threshold}" >&2
      failed=1
    fi
  done
  return "${failed}"
}

main "$@"
