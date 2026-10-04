#!/bin/sh

set -eu

compose_file="docker-compose.test.yml"
project_name="ezlb-compose-test"

cleanup() {
  docker compose -p "$project_name" -f "$compose_file" down --volumes --remove-orphans >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM
docker compose -p "$project_name" -f "$compose_file" up --build --abort-on-container-exit --exit-code-from verifier
