.PHONY: help up db-clean

help:
	@echo "make up        Build, start in the background, and delete all rocket data"
	@echo "make db-clean  Delete all rocket events and snapshots from the Compose database"

up:
	docker compose up -d --build
	$(MAKE) db-clean

db-clean:
	docker compose exec -T postgres psql -U rockets -d rockets -v ON_ERROR_STOP=1 < scripts/clean-db.sql
