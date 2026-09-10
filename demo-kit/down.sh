#!/usr/bin/env bash
set -euo pipefail
for name in beelzebub-demo-stock beelzebub-demo-fork; do
  if docker container inspect "$name" >/dev/null 2>&1; then
    # Only remove containers this kit created. A same-named container without the
    # label was started by hand; say so and stop, rather than deleting it silently
    # or failing without a message.
    if [ "$(docker inspect -f '{{index .Config.Labels "beelzebub.demo-kit"}}' "$name")" != true ]; then
      echo "$name exists but was not created by this kit (missing label beelzebub.demo-kit=true)." >&2
      echo "Remove it yourself if it is safe to do so: docker rm -f $name" >&2
      exit 1
    fi
    docker rm -f "$name"
  fi
done
if docker network inspect beelzebub-demo-kit >/dev/null 2>&1; then
  test "$(docker network inspect -f '{{index .Labels "beelzebub.demo-kit"}}' beelzebub-demo-kit)" = true
  docker network rm beelzebub-demo-kit
fi
echo "Demo containers and their logs removed. Saved evidence and binaries remain."
