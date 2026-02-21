#!/bin/bash
# muxcode-test-wrapper.sh — Run tests
#
# Wraps ./test.sh. Chaining (test->review) is handled by
# muxcode-bash-hook.sh, not this script.

cd "$(dirname "$0")/.." || exit 1

./test.sh 2>&1
