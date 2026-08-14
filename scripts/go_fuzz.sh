#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_bin="${GO:-go}"
fuzz_time="${FUZZ_TIME:-5s}"
patterns="${FUZZ_PACKAGES:-./...}"
cache_dir="${repo_root}/.gocache"
gopath_dir="${repo_root}/.gopath"
case_file="${repo_root}/.coverage/fuzz-cases.txt"

export GOCACHE="${GOCACHE:-${cache_dir}}"
export GOPATH="${GOPATH:-${gopath_dir}}"
export GOMODCACHE="${GOMODCACHE:-${gopath_dir}/pkg/mod}"

mkdir -p "$(dirname "${case_file}")" "${GOCACHE}" "${GOMODCACHE}"
: > "${case_file}"

# 此处有意按空白拆分，包模式本身不能包含空白。
# shellcheck disable=SC2086
packages="$(${go_bin} list ${patterns})"
while IFS= read -r package; do
  [[ -n "${package}" ]] || continue
  list_output="$(${go_bin} test "${package}" -run '^$' -list '^Fuzz' 2>&1)" || {
    printf '%s\n' "${list_output}" >&2
    exit 1
  }
  while IFS= read -r fuzz_name; do
    [[ "${fuzz_name}" == Fuzz* ]] || continue
    printf '%s|%s\n' "${package}" "${fuzz_name}" >> "${case_file}"
  done <<< "${list_output}"
done <<< "${packages}"

if [[ ! -s "${case_file}" ]]; then
  printf '未发现 Fuzz* 测试；至少需要一个可执行 fuzz 入口\n' >&2
  exit 1
fi

count=0
while IFS='|' read -r package fuzz_name; do
  count=$((count + 1))
  printf '[%s] 运行 %s，时长 %s\n' "${package}" "${fuzz_name}" "${fuzz_time}"
  "${go_bin}" test "${package}" -run '^$' -fuzz "^${fuzz_name}$" -fuzztime "${fuzz_time}"
done < "${case_file}"

printf '已完成 %s 个 fuzz 入口\n' "${count}"
