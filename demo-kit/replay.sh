#!/usr/bin/env bash
# Every replay is verified against newly emitted logs, then displayed.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
./verify.sh
./show.sh
