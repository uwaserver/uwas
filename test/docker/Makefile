.PHONY: test up down clean

test: up
	docker compose -f docker-compose.test.yml run --rm tester
	@$(MAKE) down

up:
	docker compose -f docker-compose.test.yml up -d --build uwas php db upstream
	@echo "Waiting for services..."
	@sleep 5

down:
	docker compose -f docker-compose.test.yml down -v

clean:
	docker compose -f docker-compose.test.yml down -v --rmi local
