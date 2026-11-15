#!/usr/bin/env bash

set -euo pipefail

fail() {
    printf 'private-network example: %s\n' "$*" >&2
    exit 1
}

verify_arrivals() {
    local path=$1 count=$2 check verdict
    check=$(jq -cn --arg path "$path" --arg id "$probe_id" --argjson count "$count" '
        {method: "GET", path: $path, header: {"X-Probe-Id": $id}, count: $count}
        + (if $count == 0 then {during: "2s"} else {within: "8s"} end)
    ')
    verdict=$(curl -q --noproxy '*' --silent --show-error --fail --max-time 10 \
        --request QUERY --header 'Content-Type: application/json' --data "$check" \
        http://127.0.0.1:8081/requests/verify)
    if ! jq -e '.passed == true' >/dev/null <<< "$verdict"; then
        jq -r '.report // .' <<< "$verdict" >&2
        fail "$path: expected $count arrivals for $probe_id"
    fi
    printf '%s: %s arrivals for %s\n' "$path" "$count" "$probe_id"
}

request() {
    local path=$1 deadline=$2
    : > "$tmp/body"
    curl_code=0
    curl -q --noproxy '*' --http1.1 --silent --show-error --max-time "$deadline" \
        --header "X-Probe-Id: $probe_id" --output "$tmp/body" \
        --write-out '%{http_code} %{time_total} %{time_starttransfer}\n' \
        "$base_url$path" > "$tmp/result" 2> "$tmp/error" || curl_code=$?
    read -r http_code elapsed first_byte < "$tmp/result"
}

expect_result() {
    local path=$1 want_curl=$2 want_http=$3
    if [[ $curl_code != "$want_curl" || $http_code != "$want_http" ]]; then
        cat "$tmp/error" >&2
        fail "$path: got curl $curl_code and HTTP $http_code, expected curl $want_curl and HTTP $want_http"
    fi
}

health() {
    request /healthz 5
    expect_result /healthz 0 200
    printf 'vlatte-lab-ok\n' | cmp -s - "$tmp/body" || fail '/healthz: unexpected response body'
}

usage='usage: probe.sh {allow|deny|restore|faults} http://host:port
       probe.sh journal {allow|deny|restore|faults} probe-id'

if [[ ${1:-} == journal ]]; then
    [[ $# == 3 && -n $3 ]] || fail "$usage"
    probe_id=$3
    case $2 in
        allow|restore) verify_arrivals /healthz 1 ;;
        deny) verify_arrivals /healthz 0 ;;
        faults)
            verify_arrivals /stall 1
            verify_arrivals /download/slow 1
            verify_arrivals /download/truncated 1
            verify_arrivals /healthz 4
            ;;
        *) fail "$usage" ;;
    esac
    exit 0
fi

[[ $# == 2 && $2 == http://?* ]] || fail "$usage"
case $1 in
    allow|deny|restore|faults) ;;
    *) fail "$usage" ;;
esac

base_url=${2%/}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/vlatte-lab.XXXXXXXXXX")
trap 'rm -rf "$tmp"' EXIT
probe_id=${tmp##*/}
printf 'on the backend, verify arrivals: bash examples/04-private-network/probe.sh journal %s %s\n' "$1" "$probe_id"

case $1 in
    allow|restore)
        health
        printf '%s: /healthz returned the expected body\n' "$1"
        ;;
    deny)
        request /healthz 5
        if [[ $http_code != 000 || ( $curl_code != 7 && $curl_code != 28 ) ]]; then
            cat "$tmp/error" >&2
            fail "deny: expected a connection failure or timeout before HTTP, got curl $curl_code and HTTP $http_code"
        fi
        printf 'deny: /healthz was unreachable (curl %s)\n' "$curl_code"
        ;;
    faults)
        fixture_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/testdata" && pwd)
        payload=$fixture_dir/payload.txt
        health

        request /stall 2
        expect_result /stall 28 000
        printf 'stall: the caller timed out before receiving headers\n'
        health

        request /download/slow 10
        expect_result /download/slow 0 200
        cmp -s "$payload" "$tmp/body" || fail '/download/slow: expected the complete payload'
        awk -v end="$elapsed" -v start="$first_byte" 'BEGIN { exit !(end - start >= 1.7) }' \
            || fail '/download/slow: the body arrived in under 1.7 seconds'
        printf 'slow download: complete payload in %ss\n' "$elapsed"
        health

        request /download/truncated 10
        expect_result /download/truncated 18 200
        head -c 64 "$payload" | cmp -s - "$tmp/body" \
            || fail '/download/truncated: expected exactly the first 64 bytes'
        printf 'truncated download: HTTP 200 with a partial transfer (64 bytes)\n'
        health
        printf 'health: a fresh request succeeded after each fault\n'
        ;;
esac
