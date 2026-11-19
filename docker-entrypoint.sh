#!/bin/sh
set -eu

# Diagnostics and one-off utility commands should not change mounted data.
case "${1##*/}" in
    sakura-main|sakura-qa)
        if [ "${2:-}" = "--version" ] || [ "${2:-}" = "--help" ]; then
            exec "$@"
        fi
        ;;
    *) exec "$@" ;;
esac

umask 077
mkdir -p /app/data/sessions /app/logs

if [ ! -e /app/data/config.json ]; then
    (set -C; printf '{}\n' > /app/data/config.json) || [ -e /app/data/config.json ]
fi
for file in prompt.txt poll_prompt.txt qa_persona.txt; do
    if [ ! -e "/app/data/$file" ]; then
        (set -C; cat "/opt/sakura/defaults/$file" > "/app/data/$file") || [ -e "/app/data/$file" ]
    fi
done

exec "$@"
