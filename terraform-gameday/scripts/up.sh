#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/env.sh"

# -- Colima -------------------------------------------------------------------
if colima list | grep -q "^${COLIMA_PROFILE}"; then
  STATUS=$(colima list | grep "^${COLIMA_PROFILE}" | awk '{print $2}')
  if [[ "$STATUS" == "Running" ]]; then
    echo "  Colima '${COLIMA_PROFILE}' already running"
  else
    echo "-> Starting Colima '${COLIMA_PROFILE}'..."
    colima start "${COLIMA_PROFILE}"
  fi
else
  echo "-> Creating Colima instance '${COLIMA_PROFILE}' (2 CPU, 4GB RAM, 20GB disk)..."
  colima start "${COLIMA_PROFILE}" --cpu 2 --memory 4 --disk 20
fi

# -- Wait for Docker socket ---------------------------------------------------
echo "-> Waiting for Docker socket..."
for i in $(seq 1 20); do
  if DOCKER_HOST="${DOCKER_HOST}" docker info &>/dev/null; then
    break
  fi
  sleep 1
done
if ! DOCKER_HOST="${DOCKER_HOST}" docker info &>/dev/null; then
  echo "ERROR: Docker socket not ready at ${DOCKER_HOST}" >&2
  exit 1
fi

# -- LocalStack ---------------------------------------------------------------
if DOCKER_HOST="${DOCKER_HOST}" docker ps --format '{{.Names}}' | grep -q "^${LOCALSTACK_CONTAINER}$"; then
  echo "  LocalStack already running"
else
  echo "-> Starting LocalStack (${LOCALSTACK_IMAGE})..."
  DOCKER_HOST="${DOCKER_HOST}" docker run -d \
    --name "${LOCALSTACK_CONTAINER}" \
    -p 4566:4566 \
    -e LOCALSTACK_HOST=localhost \
    "${LOCALSTACK_IMAGE}"
fi

echo ""
echo "Environment ready"
echo "  LocalStack endpoint : http://localhost:4566"
echo "  Docker socket       : ${DOCKER_HOST}"
echo ""
echo "  Run: source scripts/env.sh && cd scenarios/<name> && terraform init"
