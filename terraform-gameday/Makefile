.PHONY: up down status

up:
	bash scripts/up.sh

down:
	bash scripts/down.sh

status:
	@colima list | grep terraform-gameday || echo "Colima instance not found"
	@DOCKER_HOST="unix://$$HOME/.colima/terraform-gameday/docker.sock" docker ps --filter name=localstack-gameday --format "LocalStack: {{.Status}}" 2>/dev/null || echo "LocalStack not running"
