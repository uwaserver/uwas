.PHONY: test wordpress up down clean

test:
	@set -e; \
		trap '$(MAKE) down' EXIT; \
		$(MAKE) up; \
		docker compose -f docker-compose.test.yml run --rm tester

wordpress:
	./wordpress.sh

up:
	docker compose -f docker-compose.test.yml up -d --build uwas php db upstream
	@echo "Waiting for services..."
	@sleep 5

down:
	docker compose -f docker-compose.test.yml down -v

clean:
	docker compose -f docker-compose.test.yml down -v --rmi local
