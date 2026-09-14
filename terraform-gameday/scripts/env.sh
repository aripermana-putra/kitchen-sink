#!/usr/bin/env bash
# Shared environment config for the terraform-gameday practice environment

COLIMA_PROFILE="terraform-gameday"
LOCALSTACK_CONTAINER="localstack-gameday"
LOCALSTACK_IMAGE="localstack/localstack:2.3.2"
DOCKER_HOST="unix://${HOME}/.colima/${COLIMA_PROFILE}/docker.sock"

export COLIMA_PROFILE
export LOCALSTACK_CONTAINER
export LOCALSTACK_IMAGE
export DOCKER_HOST
