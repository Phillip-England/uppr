.PHONY: generate launch stop pull push

generate:
	uppr generate-server

launch: generate
	uppr launch .

stop:
	docker compose down

pull:
	@if [ -f '/home/deploy/.local/share/uppr/workspaces/personal/repos.conf' ]; then (cd '/home/deploy/.local/share/uppr/workspaces/personal' && uppr pull); fi
	@if [ -f '/home/deploy/Server/uppr/data/workspaces/chickfila/repos.conf' ]; then (cd '/home/deploy/Server/uppr/data/workspaces/chickfila' && uppr pull); fi


push:
	@test -n "$(m)" || (echo 'usage: make push m="commit message"' && exit 1)
	@if [ -f '/home/deploy/.local/share/uppr/workspaces/personal/repos.conf' ]; then (cd '/home/deploy/.local/share/uppr/workspaces/personal' && uppr push "$(m)"); fi
	@if [ -f '/home/deploy/Server/uppr/data/workspaces/chickfila/repos.conf' ]; then (cd '/home/deploy/Server/uppr/data/workspaces/chickfila' && uppr push "$(m)"); fi
