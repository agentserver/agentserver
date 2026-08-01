#!/bin/sh
set -eu

name=agentserver-v2-dev
browser_port=17444

usage() {
    printf '%s\n' 'usage: browser-url.sh [--name=container-name] [--browser-port=17444]'
}

for argument in "$@"; do
    case "${argument}" in
        --name=*) name=${argument#--name=} ;;
        --browser-port=*) browser_port=${argument#--browser-port=} ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done

case "${browser_port}" in ''|*[!0-9]*) usage >&2; exit 2 ;; esac
[ "${browser_port}" -ge 1 ] && [ "${browser_port}" -le 65535 ] || { usage >&2; exit 2; }
[ -n "${name}" ] || { usage >&2; exit 2; }

container exec "${name}" test -f /run/agentserver-v2/ready
printf 'https://127.0.0.1:%s/\n' "${browser_port}"
