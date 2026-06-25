#!/bin/sh
set -eu

# The tunnel runtime key belongs only to tunnel-client. Remove sensitive control
# plane variables before starting the unrestricted coding runtime.
unset CONTROL_PLANE_API_KEY OPENAI_ADMIN_KEY

export HOME=/home/dev
export USER=dev
export LOGNAME=dev
export SHELL=/bin/bash

exec su-exec dev:dev /usr/local/bin/codeforge-mcp
