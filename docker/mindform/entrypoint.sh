#!/bin/sh
set -eu

mkdir -p /eidos/run/wake
mkdir -p /eidos/claude
exec /usr/local/bin/eidos supervisor run
