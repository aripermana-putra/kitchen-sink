#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/env.sh"

echo "-> Stopping LocalStack..."
DOCKER_HOST="${DOCKER_HOST}" docker stop "${LOCALSTACK_CONTAINER}" 2>/dev/null || true
DOCKER_HOST="${DOCKER_HOST}" docker rm "${LOCALSTACK_CONTAINER}" 2>/dev/null || true

echo "-> Stopping Colima '${COLIMA_PROFILE}'..."
colima stop "${COLIMA_PROFILE}" 2>/dev/null || true

echo "Environment stopped"
echo ""
echo "  To delete the Colima VM entirely:"
echo "  colima delete ${COLIMA_PROFILE}"
