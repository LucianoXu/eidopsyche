#!/bin/sh
# Minimal stub of `claude` for forge_plan_dream integration tests.
# Echoes its -p argument and exits 0. Swallows other flags.
while [ $# -gt 0 ]; do
  case "$1" in
    -p)
      shift
      printf '%s\n' "$1"
      shift
      ;;
    --append-system-prompt|--model)
      shift; shift
      ;;
    --dangerously-skip-permissions)
      shift
      ;;
    *)
      shift
      ;;
  esac
done
exit 0
