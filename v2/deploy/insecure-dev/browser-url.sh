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
browser_token=$(container exec "${name}" /bin/cat /var/lib/agentserver/stack/secrets/browser-bearer.token)
case "${browser_token}" in
    asv2dev-browser-*) ;;
    *) printf '%s\n' 'browser-url.sh: container returned an invalid development bearer' >&2; exit 1 ;;
esac
browser_entropy=${browser_token#asv2dev-browser-}
case "${browser_entropy}" in
    ''|*[!A-Za-z0-9_-]*) printf '%s\n' 'browser-url.sh: container returned a non-canonical development bearer' >&2; exit 1 ;;
esac

printf '%s\n' 'browser-url.sh: INSECURE DEV URL; the fragment is scrubbed into page memory on load'
printf 'https://127.0.0.1:%s/#token=%s\n' "${browser_port}" "${browser_token}"
