.PHONY: generate launch stop pull push

generate:
	uppr generate-server

launch: generate
	services="$$(docker compose config --services | grep -v '^uppr$$')" && docker compose up --build -d --remove-orphans --no-deps $$services

stop:
	docker compose down

pull:
	@if [ -f '/home/deploy/.local/share/uppr/workspaces/personal/repos.conf' ]; then (cd '/home/deploy/.local/share/uppr/workspaces/personal' && uppr pull); fi


push:
	@test -n "$(m)" || (echo 'usage: make push m="commit message"' && exit 1)
	@if [ -f '/home/deploy/.local/share/uppr/workspaces/personal/repos.conf' ]; then (cd '/home/deploy/.local/share/uppr/workspaces/personal' && uppr push "$(m)"); fi
