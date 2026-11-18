.PHONY: up down logs check test smoke
up:
	docker compose up --build -d
down:
	docker compose down
logs:
	docker compose logs -f --tail=100 api worker destination
check:
	docker compose config --quiet
	sh -n db/00-roles.sh
test:
	docker compose build api
smoke:
	docker compose exec -T api relaydesk -mode smoke
